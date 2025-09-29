//go:build linux
// +build linux

package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// LinuxTrafficRedirectionManager manages iptables rules for traffic redirection on Linux.
type LinuxTrafficRedirectionManager struct {
	rules map[string]*IptablesRule
	mu    sync.RWMutex
}

// IptablesRule represents an iptables redirection rule.
type IptablesRule struct {
	FromPort int    // Source port
	ToPort   int    // Destination port
	FromIP   string // Source IP
	ToIP     string // Destination IP
	RuleText string // The actual iptables rule text
	Added    bool   // Whether this rule was added by us
}

// NewTrafficRedirectionProvider creates a new Linux traffic redirection manager.
func NewTrafficRedirectionProvider() TrafficRedirectionProvider {
	return &LinuxTrafficRedirectionManager{
		rules: make(map[string]*IptablesRule),
	}
}

// IsSupported checks if traffic redirection is supported on Linux.
func (ipt *LinuxTrafficRedirectionManager) IsSupported() bool {
	if runtime.GOOS != osLinux {
		return false
	}

	// Check if iptables command is available
	_, err := exec.LookPath("iptables")
	return err == nil
}

// CheckRequirements verifies that iptables can be used.
func (ipt *LinuxTrafficRedirectionManager) CheckRequirements() error {
	if runtime.GOOS != osLinux {
		return errors.New("iptables is only supported on Linux")
	}

	// Check if iptables command is available
	_, err := exec.LookPath("iptables")
	if err != nil {
		return fmt.Errorf("iptables command not found: %w", err)
	}

	// Check if iptables can be run (requires sudo)
	cmd := exec.Command("iptables", "-L", "-n")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run iptables (may require sudo): %w", err)
	}

	return nil
}

// AddRedirectionRule adds an iptables redirection rule.
func (ipt *LinuxTrafficRedirectionManager) AddRedirectionRule(
	fromPort, toPort int,
	fromIP, toIP string,
) error {
	ipt.mu.Lock()
	defer ipt.mu.Unlock()

	// Create unique key for this rule
	key := fmt.Sprintf("%s:%d->%s:%d", fromIP, fromPort, toIP, toPort)

	// Check if rule already exists
	if _, exists := ipt.rules[key]; exists {
		logger.Log.WithFields(logrus.Fields{
			"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
			"to":   fmt.Sprintf("%s:%d", toIP, toPort),
		}).Warn("Iptables redirection rule already exists")
		return nil
	}

	// Create iptables rule for port redirection (DNAT)
	// Example: iptables -t nat -A OUTPUT -p tcp --dst 127.0.0.2 --dport 8080 -j DNAT --to-destination 127.0.0.1:8888

	// Add DNAT rule in OUTPUT chain for locally generated traffic
	dnatRule := []string{
		"-t", "nat",
		"-A", "OUTPUT",
		"-p", "tcp",
		"--dst", fromIP,
		"--dport", strconv.Itoa(fromPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", toIP, toPort),
	}

	cmd := exec.Command("sudo", append([]string{"iptables"}, dnatRule...)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add iptables DNAT rule: %w", err)
	}

	// Add DNAT rule in PREROUTING chain for forwarded traffic
	preroutingRule := []string{
		"-t", "nat",
		"-A", "PREROUTING",
		"-p", "tcp",
		"--dst", fromIP,
		"--dport", strconv.Itoa(fromPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", toIP, toPort),
	}

	cmd = exec.Command("sudo", append([]string{"iptables"}, preroutingRule...)...)
	if err := cmd.Run(); err != nil {
		// Try to clean up the OUTPUT rule we just added
		if err := ipt.removeOutputRule(fromPort, fromIP, toIP, toPort); err != nil {
			logger.Log.WithError(err).
				Warn("Failed to cleanup OUTPUT rule after PREROUTING rule failure")
		}
		return fmt.Errorf("failed to add iptables PREROUTING rule: %w", err)
	}

	ruleText := fmt.Sprintf("DNAT %s:%d -> %s:%d", fromIP, fromPort, toIP, toPort)

	// Store the rule information
	ipt.rules[key] = &IptablesRule{
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
	}).Info("Added iptables redirection rule")

	return nil
}

// RemoveRedirectionRule removes an iptables redirection rule.
func (ipt *LinuxTrafficRedirectionManager) RemoveRedirectionRule(
	fromPort, toPort int,
	fromIP, toIP string,
) error {
	ipt.mu.Lock()
	defer ipt.mu.Unlock()

	key := fmt.Sprintf("%s:%d->%s:%d", fromIP, fromPort, toIP, toPort)
	rule, exists := ipt.rules[key]
	if !exists {
		logger.Log.WithFields(logrus.Fields{
			"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
			"to":   fmt.Sprintf("%s:%d", toIP, toPort),
		}).Warn("Iptables rule not found for removal")
		return nil
	}

	// Remove both rules
	if err := ipt.removeOutputRule(fromPort, fromIP, toIP, toPort); err != nil {
		logger.Log.WithError(err).Warn("Failed to remove OUTPUT rule")
	}

	if err := ipt.removePreRoutingRule(fromPort, fromIP, toIP, toPort); err != nil {
		logger.Log.WithError(err).Warn("Failed to remove PREROUTING rule")
	}

	delete(ipt.rules, key)

	logger.Log.WithFields(logrus.Fields{
		"rule": rule.RuleText,
		"from": fmt.Sprintf("%s:%d", fromIP, fromPort),
		"to":   fmt.Sprintf("%s:%d", toIP, toPort),
	}).Info("Removed iptables redirection rule")

	return nil
}

// removeOutputRule removes the OUTPUT chain rule.
func (ipt *LinuxTrafficRedirectionManager) removeOutputRule(
	fromPort int,
	fromIP, toIP string,
	toPort int,
) error {
	cmd := exec.Command("sudo", "iptables",
		"-t", "nat",
		"-D", "OUTPUT",
		"-p", "tcp",
		"--dst", fromIP,
		"--dport", strconv.Itoa(fromPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", toIP, toPort),
	)
	return cmd.Run()
}

// removePreRoutingRule removes the PREROUTING chain rule.
func (ipt *LinuxTrafficRedirectionManager) removePreRoutingRule(
	fromPort int,
	fromIP, toIP string,
	toPort int,
) error {
	cmd := exec.Command("sudo", "iptables",
		"-t", "nat",
		"-D", "PREROUTING",
		"-p", "tcp",
		"--dst", fromIP,
		"--dport", strconv.Itoa(fromPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", toIP, toPort),
	)
	return cmd.Run()
}

// ListRedirectionRules lists all active redirection rules.
func (ipt *LinuxTrafficRedirectionManager) ListRedirectionRules() ([]string, error) {
	ipt.mu.RLock()
	defer ipt.mu.RUnlock()

	var rules []string
	for _, rule := range ipt.rules {
		rules = append(rules, rule.RuleText)
	}
	return rules, nil
}

// Cleanup removes all redirection rules added by this manager.
func (ipt *LinuxTrafficRedirectionManager) Cleanup() error {
	ipt.mu.Lock()
	defer ipt.mu.Unlock()

	var errors []string
	for key, rule := range ipt.rules {
		if err := ipt.removeOutputRule(rule.FromPort, rule.FromIP, rule.ToIP, rule.ToPort); err != nil {
			errors = append(errors, fmt.Sprintf("failed to cleanup OUTPUT rule %s: %v", key, err))
		}
		if err := ipt.removePreRoutingRule(rule.FromPort, rule.FromIP, rule.ToIP, rule.ToPort); err != nil {
			errors = append(
				errors,
				fmt.Sprintf("failed to cleanup PREROUTING rule %s: %v", key, err),
			)
		}
	}

	// Clear our rule tracking
	ipt.rules = make(map[string]*IptablesRule)

	if len(errors) > 0 {
		logger.Log.WithField("errors", strings.Join(errors, ", ")).
			Warn("Some iptables cleanup errors occurred")
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, ", "))
	}

	logger.Log.Info("Iptables cleanup completed")
	return nil
}
