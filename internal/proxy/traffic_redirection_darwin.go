//go:build darwin
// +build darwin

package proxy

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// DarwinTrafficRedirectionManager manages pfctl rules for traffic redirection on macOS
type DarwinTrafficRedirectionManager struct {
	rules map[string]*DarwinPfctlRule
	mu    sync.RWMutex
}

// DarwinPfctlRule represents a pfctl redirection rule
type DarwinPfctlRule struct {
	FromPort int    // Source port
	ToPort   int    // Destination port
	FromIP   string // Source IP
	ToIP     string // Destination IP
	RuleText string // The actual pfctl rule text
	Added    bool   // Whether this rule was added by us
}

// NewTrafficRedirectionProvider creates a new macOS traffic redirection manager
func NewTrafficRedirectionProvider() TrafficRedirectionProvider {
	return &DarwinTrafficRedirectionManager{
		rules: make(map[string]*DarwinPfctlRule),
	}
}

// IsSupported checks if traffic redirection is supported on macOS
func (pfm *DarwinTrafficRedirectionManager) IsSupported() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Check if pfctl command is available
	_, err := exec.LookPath("pfctl")
	return err == nil
}

// CheckRequirements verifies that pfctl can be used
func (pfm *DarwinTrafficRedirectionManager) CheckRequirements() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("pfctl is only supported on macOS")
	}

	// Check if pfctl command is available
	_, err := exec.LookPath("pfctl")
	if err != nil {
		return fmt.Errorf("pfctl command not found: %w", err)
	}

	// Check if pfctl can be run (requires sudo)
	cmd := exec.Command("pfctl", "-sr")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run pfctl (may require sudo): %w", err)
	}

	return nil
}

// AddRedirectionRule adds a pfctl redirection rule
func (pfm *DarwinTrafficRedirectionManager) AddRedirectionRule(fromPort, toPort int, fromIP, toIP string) error {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()

	// Create unique key for this rule
	key := fmt.Sprintf("%s:%d->%s:%d", fromIP, fromPort, toIP, toPort)

	// Check if rule already exists
	if _, exists := pfm.rules[key]; exists {
		logger.Log.WithFields(logrus.Fields{
			"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
			"to":   fmt.Sprintf("%s:%d", toIP, toPort),
		}).Warn("Pfctl redirection rule already exists")
		return nil
	}

	// Create pfctl rule for port redirection
	// Example: rdr pass on lo0 inet proto tcp from any to 127.0.0.2 port 8080 -> 127.0.0.1 port 8888
	ruleText := fmt.Sprintf("rdr pass on lo0 inet proto tcp from any to %s port %d -> %s port %d",
		fromIP, fromPort, toIP, toPort)

	// Add the rule to pfctl
	cmd := exec.Command("sudo", "pfctl", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleText + "\n")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add pfctl rule: %w", err)
	}

	// Store the rule information
	pfm.rules[key] = &DarwinPfctlRule{
		FromPort: fromPort,
		ToPort:   toPort,
		FromIP:   fromIP,
		ToIP:     toIP,
		RuleText: ruleText,
		Added:    true,
	}

	logger.Log.WithFields(logrus.Fields{
		"rule": ruleText,
		"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
		"to":   fmt.Sprintf("%s:%d", toIP, toPort),
	}).Info("Added pfctl redirection rule")

	return nil
}

// RemoveRedirectionRule removes a pfctl redirection rule
func (pfm *DarwinTrafficRedirectionManager) RemoveRedirectionRule(fromPort, toPort int, fromIP, toIP string) error {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()

	key := fmt.Sprintf("%s:%d->%s:%d", fromIP, fromPort, toIP, toPort)
	rule, exists := pfm.rules[key]
	if !exists {
		logger.Log.WithFields(logrus.Fields{
			"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
			"to":   fmt.Sprintf("%s:%d", toIP, toPort),
		}).Warn("Pfctl rule not found for removal")
		return nil
	}

	// Note: pfctl doesn't support individual rule removal easily
	// For now, we'll mark the rule as removed and rely on pfctl flush or system restart
	// In a production system, you might want to maintain a pfctl config file

	delete(pfm.rules, key)

	logger.Log.WithFields(logrus.Fields{
		"rule": rule.RuleText,
		"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
		"to":   fmt.Sprintf("%s:%d", toIP, toPort),
	}).Info("Removed pfctl redirection rule")

	return nil
}

// ListRedirectionRules lists all active redirection rules
func (pfm *DarwinTrafficRedirectionManager) ListRedirectionRules() ([]string, error) {
	pfm.mu.RLock()
	defer pfm.mu.RUnlock()

	var rules []string
	for _, rule := range pfm.rules {
		rules = append(rules, rule.RuleText)
	}
	return rules, nil
}

// Cleanup removes all redirection rules added by this manager
func (pfm *DarwinTrafficRedirectionManager) Cleanup() error {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()

	// Flush all pfctl rules (this is a bit aggressive, but ensures cleanup)
	// In production, you might want a more surgical approach
	cmd := exec.Command("sudo", "pfctl", "-F", "nat")
	if err := cmd.Run(); err != nil {
		logger.Log.WithError(err).Warn("Failed to flush pfctl nat rules")
		return fmt.Errorf("failed to cleanup pfctl rules: %w", err)
	}

	// Clear our rule tracking
	pfm.rules = make(map[string]*DarwinPfctlRule)

	logger.Log.Info("Pfctl cleanup completed")
	return nil
}
