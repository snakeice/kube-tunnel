package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// PfctlManager manages pfctl (Packet Filter) rules on macOS for traffic redirection.
type PfctlManager struct {
	rules    []string
	anchors  []string
	mu       sync.RWMutex
	enabled  bool
	rulePath string
}

// PfctlRule represents a pfctl rule configuration.
type PfctlRule struct {
	From        string // Source (interface, IP, port)
	To          string // Destination (IP, port)
	Protocol    string // tcp, udp, etc.
	Action      string // rdr (redirect), nat, etc.
	Interface   string // Network interface
	Description string // Rule description
}

// NewPfctlManager creates a new pfctl manager.
func NewPfctlManager() *PfctlManager {
	return &PfctlManager{
		rules:    make([]string, 0),
		anchors:  make([]string, 0),
		enabled:  false,
		rulePath: "/tmp/kube-tunnel-pf.conf",
	}
}

// IsSupported checks if pfctl is available on the system.
func (pf *PfctlManager) IsSupported() bool {
	if runtime.GOOS != osDarwin {
		return false
	}

	// Check if pfctl is available
	_, err := exec.LookPath("pfctl")
	return err == nil
}

// CheckRequirements verifies that pfctl can be used.
func (pf *PfctlManager) CheckRequirements() error {
	if runtime.GOOS != osDarwin {
		return errors.New("pfctl is only available on macOS")
	}

	// Check if pfctl is available
	if _, err := exec.LookPath("pfctl"); err != nil {
		return fmt.Errorf("pfctl command not found: %w", err)
	}

	// Test basic pfctl functionality (requires sudo)
	cmd := exec.Command("sudo", "pfctl", "-s", "info")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pfctl requires sudo privileges: %w", err)
	}

	return nil
}

// AddRedirectionRule adds a traffic redirection rule.
func (pf *PfctlManager) AddRedirectionRule(rule PfctlRule) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if !pf.IsSupported() {
		return errors.New("pfctl is not supported on this system")
	}

	// Create pfctl rule string
	// Example: rdr on lo0 inet proto tcp from any to 127.0.0.2 port 8080 -> 127.0.0.1 port 80
	ruleStr := pf.buildRuleString(rule)

	// Add rule to our list
	pf.rules = append(pf.rules, ruleStr)

	// Apply the rules
	if err := pf.applyRules(); err != nil {
		// Remove the rule we just added if applying failed
		pf.rules = pf.rules[:len(pf.rules)-1]
		return fmt.Errorf("failed to apply pfctl rule: %w", err)
	}

	logger.Log.WithFields(logrus.Fields{
		"from":        rule.From,
		"to":          rule.To,
		"protocol":    rule.Protocol,
		"interface":   rule.Interface,
		"description": rule.Description,
	}).Info("🔀 Added pfctl redirection rule")

	return nil
}

// RemoveRedirectionRule removes a specific redirection rule.
func (pf *PfctlManager) RemoveRedirectionRule(rule PfctlRule) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	ruleStr := pf.buildRuleString(rule)

	// Find and remove the rule
	for i, existingRule := range pf.rules {
		if existingRule == ruleStr {
			pf.rules = append(pf.rules[:i], pf.rules[i+1:]...)
			break
		}
	}

	// Apply the updated rules
	if err := pf.applyRules(); err != nil {
		return fmt.Errorf("failed to apply updated pfctl rules: %w", err)
	}

	logger.Log.WithFields(logrus.Fields{
		"from": rule.From,
		"to":   rule.To,
	}).Info("🗑️  Removed pfctl redirection rule")

	return nil
}

// AddPortRedirection adds a simple port redirection rule.
func (pf *PfctlManager) AddPortRedirection(
	fromIP string,
	fromPort int,
	toIP string,
	toPort int,
) error {
	rule := PfctlRule{
		From:        fmt.Sprintf("%s:%d", fromIP, fromPort),
		To:          fmt.Sprintf("%s:%d", toIP, toPort),
		Protocol:    "tcp",
		Action:      "rdr",
		Interface:   "lo0",
		Description: fmt.Sprintf("Redirect %s:%d to %s:%d", fromIP, fromPort, toIP, toPort),
	}

	return pf.AddRedirectionRule(rule)
}

// FlushRules removes all pfctl rules managed by this instance.
func (pf *PfctlManager) FlushRules() error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if !pf.enabled {
		return nil
	}

	// Disable pfctl
	if err := pf.disable(); err != nil {
		logger.LogError("Failed to disable pfctl", err)
	}

	pf.rules = make([]string, 0)
	pf.enabled = false

	logger.Log.Info("🧹 Flushed all pfctl rules")
	return nil
}

// GetRules returns all active rules.
func (pf *PfctlManager) GetRules() []string {
	pf.mu.RLock()
	defer pf.mu.RUnlock()

	return append([]string{}, pf.rules...)
}

// IsEnabled returns whether pfctl is currently enabled.
func (pf *PfctlManager) IsEnabled() bool {
	pf.mu.RLock()
	defer pf.mu.RUnlock()

	return pf.enabled
}

// GetStatistics returns pfctl statistics.
func (pf *PfctlManager) GetStatistics() map[string]any {
	pf.mu.RLock()
	defer pf.mu.RUnlock()

	stats := map[string]any{
		"supported":     pf.IsSupported(),
		"enabled":       pf.enabled,
		"rules_count":   len(pf.rules),
		"anchors_count": len(pf.anchors),
		"platform":      runtime.GOOS,
		"rule_path":     pf.rulePath,
	}

	if pf.IsSupported() {
		// Try to get pfctl info (may require sudo)
		cmd := exec.Command("pfctl", "-s", "info")
		if output, err := cmd.Output(); err == nil {
			stats["pfctl_info"] = strings.TrimSpace(string(output))
		}
	}

	return stats
}

// buildRuleString constructs a pfctl rule string from a PfctlRule.
func (pf *PfctlManager) buildRuleString(rule PfctlRule) string {
	// Parse from and to addresses
	fromParts := strings.Split(rule.From, ":")
	toParts := strings.Split(rule.To, ":")

	fromIP := fromParts[0]
	fromPort := ""
	if len(fromParts) > 1 {
		fromPort = fromParts[1]
	}

	toIP := toParts[0]
	toPort := ""
	if len(toParts) > 1 {
		toPort = toParts[1]
	}

	// Build rule string
	// Format: rdr on [interface] inet proto [protocol] from any to [fromIP] port [fromPort] -> [toIP] port [toPort]
	ruleStr := fmt.Sprintf("%s on %s inet proto %s from any to %s",
		rule.Action, rule.Interface, rule.Protocol, fromIP)

	if fromPort != "" {
		ruleStr += fmt.Sprintf(" port %s", fromPort)
	}

	ruleStr += fmt.Sprintf(" -> %s", toIP)

	if toPort != "" {
		ruleStr += fmt.Sprintf(" port %s", toPort)
	}

	return ruleStr
}

// applyRules writes rules to a config file and loads them with pfctl.
func (pf *PfctlManager) applyRules() error {
	// Create rule file content
	content := "# kube-tunnel pfctl rules\n"
	for _, rule := range pf.rules {
		content += rule + "\n"
	}

	// Write rules to temporary file
	if err := pf.writeRuleFile(content); err != nil {
		return fmt.Errorf("failed to write rule file: %w", err)
	}

	// Load rules with pfctl
	if err := pf.loadRules(); err != nil {
		return fmt.Errorf("failed to load pfctl rules: %w", err)
	}

	if !pf.enabled {
		// Enable pfctl
		if err := pf.enable(); err != nil {
			return fmt.Errorf("failed to enable pfctl: %w", err)
		}
		pf.enabled = true
	}

	return nil
}

// writeRuleFile writes pfctl rules to a configuration file.
func (pf *PfctlManager) writeRuleFile(content string) error {
	cmd := exec.Command("sudo", "tee", pf.rulePath)
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

// loadRules loads rules from the configuration file.
func (pf *PfctlManager) loadRules() error {
	// Load rules: sudo pfctl -f /tmp/kube-tunnel-pf.conf
	cmd := exec.Command("sudo", "pfctl", "-f", pf.rulePath)
	return cmd.Run()
}

// enable enables pfctl.
func (pf *PfctlManager) enable() error {
	// Enable pfctl: sudo pfctl -e
	cmd := exec.Command("sudo", "pfctl", "-e")
	return cmd.Run()
}

// disable disables pfctl.
func (pf *PfctlManager) disable() error {
	// Disable pfctl: sudo pfctl -d
	cmd := exec.Command("sudo", "pfctl", "-d")
	return cmd.Run()
}

// TestRule tests if a pfctl rule syntax is valid.
func (pf *PfctlManager) TestRule(rule PfctlRule) error {
	ruleStr := pf.buildRuleString(rule)

	// Test rule syntax by parsing it (dry run)
	cmd := exec.Command("sudo", "pfctl", "-nf", "-")
	cmd.Stdin = strings.NewReader(ruleStr)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invalid pfctl rule syntax: %w", err)
	}

	return nil
}

// CreateAnchor creates a pfctl anchor for better rule organization.
func (pf *PfctlManager) CreateAnchor(anchorName string) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	// Add anchor to our list
	pf.anchors = append(pf.anchors, anchorName)

	// Apply anchor configuration
	anchorRule := fmt.Sprintf("anchor \"%s\"", anchorName)
	pf.rules = append([]string{anchorRule}, pf.rules...)

	if err := pf.applyRules(); err != nil {
		// Remove the anchor if applying failed
		pf.anchors = pf.anchors[:len(pf.anchors)-1]
		pf.rules = pf.rules[1:] // Remove the anchor rule
		return fmt.Errorf("failed to create pfctl anchor: %w", err)
	}

	logger.Log.WithField("anchor", anchorName).Info("🔗 Created pfctl anchor")
	return nil
}

// RemoveAnchor removes a pfctl anchor.
func (pf *PfctlManager) RemoveAnchor(anchorName string) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	// Remove anchor from our list
	for i, anchor := range pf.anchors {
		if anchor == anchorName {
			pf.anchors = append(pf.anchors[:i], pf.anchors[i+1:]...)
			break
		}
	}

	// Remove anchor rule
	anchorRule := fmt.Sprintf("anchor \"%s\"", anchorName)
	for i, rule := range pf.rules {
		if rule == anchorRule {
			pf.rules = append(pf.rules[:i], pf.rules[i+1:]...)
			break
		}
	}

	// Apply updated rules
	if err := pf.applyRules(); err != nil {
		return fmt.Errorf("failed to remove pfctl anchor: %w", err)
	}

	logger.Log.WithField("anchor", anchorName).Info("🗑️  Removed pfctl anchor")
	return nil
}
