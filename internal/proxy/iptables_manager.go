package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// IPTablesManager manages iptables rules for port redirection
// This is an optional advanced feature for system-level port redirection.
type IPTablesManager struct {
	mainPort int
	rules    []string
}

// NewIPTablesManager creates a new iptables manager.
func NewIPTablesManager(mainPort int) *IPTablesManager {
	return &IPTablesManager{
		mainPort: mainPort,
		rules:    make([]string, 0),
	}
}

// AddPortRedirection adds an iptables rule to redirect a port to the main proxy
// This requires root privileges and iptables to be available.
func (ipt *IPTablesManager) AddPortRedirection(fromPort int) error {
	if !ipt.isIptablesAvailable() {
		return errors.New("iptables is not available or requires root privileges")
	}

	rule := fmt.Sprintf("REDIRECT --to-ports %d", ipt.mainPort)

	// Add PREROUTING rule for incoming traffic
	cmd := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", strconv.Itoa(fromPort),
		"-j", rule)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add iptables rule for port %d: %w", fromPort, err)
	}

	// Add OUTPUT rule for local traffic
	cmd = exec.Command("iptables", "-t", "nat", "-A", "OUTPUT",
		"-p", "tcp", "--dport", strconv.Itoa(fromPort),
		"-j", rule)

	if err := cmd.Run(); err != nil {
		// Try to remove the PREROUTING rule we just added
		if cleanupErr := ipt.removePortRedirection(fromPort); cleanupErr != nil {
			logger.Log.Warnf(
				"Failed to cleanup port redirection after OUTPUT rule failure: %v",
				cleanupErr,
			)
		}
		return fmt.Errorf("failed to add iptables OUTPUT rule for port %d: %w", fromPort, err)
	}

	ruleStr := fmt.Sprintf("%d->%d", fromPort, ipt.mainPort)
	ipt.rules = append(ipt.rules, ruleStr)

	logger.Log.Infof("🔧 Added iptables redirection: port %d -> %d", fromPort, ipt.mainPort)
	return nil
}

// RemovePortRedirection removes an iptables rule for port redirection.
func (ipt *IPTablesManager) RemovePortRedirection(fromPort int) error {
	if !ipt.isIptablesAvailable() {
		return errors.New("iptables is not available or requires root privileges")
	}

	return ipt.removePortRedirection(fromPort)
}

func (ipt *IPTablesManager) removePortRedirection(fromPort int) error {
	rule := fmt.Sprintf("REDIRECT --to-ports %d", ipt.mainPort)

	// Remove PREROUTING rule
	cmd := exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
		"-p", "tcp", "--dport", strconv.Itoa(fromPort),
		"-j", rule)
	if runErr := cmd.Run(); runErr != nil {
		logger.Log.Debugf("Failed to remove PREROUTING rule (this may be normal): %v", runErr)
	} // Ignore errors for cleanup

	// Remove OUTPUT rule
	cmd = exec.Command("iptables", "-t", "nat", "-D", "OUTPUT",
		"-p", "tcp", "--dport", strconv.Itoa(fromPort),
		"-j", rule)
	if runErr := cmd.Run(); runErr != nil {
		logger.Log.Debugf("Failed to remove OUTPUT rule (this may be normal): %v", runErr)
	} // Ignore errors for cleanup

	// Remove from rules list
	ruleStr := fmt.Sprintf("%d->%d", fromPort, ipt.mainPort)
	for i, r := range ipt.rules {
		if r == ruleStr {
			ipt.rules = append(ipt.rules[:i], ipt.rules[i+1:]...)
			break
		}
	}

	logger.Log.Infof("🔧 Removed iptables redirection: port %d -> %d", fromPort, ipt.mainPort)
	return nil
}

// CleanupAll removes all iptables rules added by this manager.
func (ipt *IPTablesManager) CleanupAll() error {
	if !ipt.isIptablesAvailable() {
		return errors.New("iptables is not available or requires root privileges")
	}

	var errors []error
	for _, ruleStr := range ipt.rules {
		parts := strings.Split(ruleStr, "->")
		if len(parts) == 2 {
			if fromPort, err := strconv.Atoi(parts[0]); err == nil {
				if err := ipt.removePortRedirection(fromPort); err != nil {
					errors = append(errors, err)
				}
			}
		}
	}

	ipt.rules = make([]string, 0)

	if len(errors) > 0 {
		return fmt.Errorf("failed to cleanup some iptables rules: %v", errors)
	}

	logger.Log.Info("🔧 Cleaned up all iptables redirection rules")
	return nil
}

// GetRules returns a list of active iptables rules.
func (ipt *IPTablesManager) GetRules() []string {
	return append([]string{}, ipt.rules...)
}

func (ipt *IPTablesManager) isIptablesAvailable() bool {
	cmd := exec.Command("iptables", "--version")
	return cmd.Run() == nil
}
