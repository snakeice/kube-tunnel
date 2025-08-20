package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// IPRangeManager manages allocation of /32 IP ranges for virtual interfaces.
type IPRangeManager struct {
	mu              sync.RWMutex
	allocatedRanges map[string]*IPRange
	preferredRanges []string
}

// IPRange represents a /32 IP range allocation.
type IPRange struct {
	BaseIP        string
	InterfaceName string
	Allocated     bool
	Created       time.Time
	LastUsed      time.Time
	PortForwards  map[int]string // port -> target mapping
	mu            sync.RWMutex
}

// AllocateFreeIPRange finds and allocates a free /32 IP range.
func (irm *IPRangeManager) AllocateFreeIPRange(interfacePrefix string) (*IPRange, error) {
	irm.mu.Lock()
	defer irm.mu.Unlock()

	// Ultra-fast path: try sequential allocation from preferred ranges
	return irm.allocateSequentialFast(interfacePrefix)
}

// allocateSequentialFast uses optimistic allocation with minimal checking.
func (irm *IPRangeManager) allocateSequentialFast(interfacePrefix string) (*IPRange, error) {
	// Create extended candidate list for faster searching
	candidates := irm.generateFastCandidates()

	// Try candidates with minimal validation (optimistic approach)
	for _, baseIP := range candidates {
		// Skip if already allocated
		if _, exists := irm.allocatedRanges[baseIP]; exists {
			continue
		}

		// Quick availability check - only ping test with very short timeout
		if irm.isIPLikelyAvailable(baseIP) {
			interfaceName := fmt.Sprintf("%s%d", interfacePrefix, len(irm.allocatedRanges))
			ipRange := &IPRange{
				BaseIP:        baseIP,
				InterfaceName: interfaceName,
				Allocated:     true,
				Created:       time.Now(),
				LastUsed:      time.Now(),
				PortForwards:  make(map[int]string),
			}

			irm.allocatedRanges[baseIP] = ipRange
			logger.Log.Infof("Allocated IP range %s for interface %s", baseIP, interfaceName)
			return ipRange, nil
		}
	}

	return nil, fmt.Errorf(
		"no available IP ranges found after trying %d candidates",
		len(candidates),
	)
}

// generateFastCandidates creates an optimized list prioritizing likely available IPs.
func (irm *IPRangeManager) generateFastCandidates() []string {
	var candidates []string

	// Start with preferred ranges
	candidates = append(candidates, irm.preferredRanges...)

	// Add quick-scan ranges in link-local space (most likely to be free)
	for i := 105; i <= 115; i++ {
		for j := 1; j <= 10; j++ {
			candidates = append(candidates, fmt.Sprintf("169.254.%d.%d", i, j))
		}
	}

	// Add some high-numbered ranges (usually free)
	for i := 1; i <= 5; i++ {
		candidates = append(candidates, fmt.Sprintf("169.254.200.%d", i))
		candidates = append(candidates, fmt.Sprintf("169.254.250.%d", i))
		candidates = append(candidates, fmt.Sprintf("127.200.0.%d", i))
	}

	return candidates
}

// isIPLikelyAvailable performs minimal checking for fast allocation.
func (irm *IPRangeManager) isIPLikelyAvailable(ip string) bool {
	// Only do a very fast ping test with minimal timeout
	// Skip binding test for speed - will be validated during actual interface creation
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "1", "-w", "1", ip)
	cmd.Env = []string{} // Minimal environment
	err := cmd.Run()

	// If ping fails (which is good - means IP likely not in use), return true
	// If ping succeeds or times out, return false
	return err != nil
}

// ReleaseIPRange releases an allocated IP range.
func (irm *IPRangeManager) ReleaseIPRange(baseIP string) error {
	irm.mu.Lock()
	defer irm.mu.Unlock()

	ipRange, exists := irm.allocatedRanges[baseIP]
	if !exists {
		return fmt.Errorf("IP range %s not allocated", baseIP)
	}

	// Clean up any port forwards
	ipRange.mu.Lock()
	for port := range ipRange.PortForwards {
		if err := irm.stopPortForward(ipRange, port); err != nil {
			logger.Log.Warnf("Failed to stop port forward on port %d: %v", port, err)
		}
	}
	ipRange.mu.Unlock()

	delete(irm.allocatedRanges, baseIP)
	logger.Log.Infof("Released IP range %s (interface %s)", baseIP, ipRange.InterfaceName)
	return nil
}

// CreateVirtualInterface creates a virtual interface for the IP range.
func (ir *IPRange) CreateVirtualInterface() error {
	logger.Log.Infof("Creating virtual interface %s with IP %s", ir.InterfaceName, ir.BaseIP)

	// Validate interface name to prevent injection
	validName := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validName.MatchString(ir.InterfaceName) {
		return fmt.Errorf("invalid interface name: %s", ir.InterfaceName)
	}

	cmds := [][]string{
		{"sudo", "ip", "link", "add", ir.InterfaceName, "type", "dummy"},
		{"sudo", "ip", "addr", "add", ir.BaseIP + "/32", "dev", ir.InterfaceName},
		{"sudo", "ip", "link", "set", ir.InterfaceName, "up"},
	}

	for _, cmd := range cmds {
		logger.Log.Debugf("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("command failed: %w, output: %s", err, string(out))
		}
		logger.Log.Debugf("Command output: %s", string(out))
	}

	// Verify interface creation
	if err := ir.verifyInterface(); err != nil {
		return fmt.Errorf("interface verification failed: %w", err)
	}

	logger.Log.Infof(
		"Virtual interface %s created successfully with IP %s",
		ir.InterfaceName,
		ir.BaseIP,
	)
	return nil
}

// verifyInterface verifies that the virtual interface was created correctly.
func (ir *IPRange) verifyInterface() error {
	// Check if interface exists and is up
	cmd := exec.Command("ip", "link", "show", ir.InterfaceName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("interface %s not found: %w", ir.InterfaceName, err)
	}

	if !strings.Contains(string(out), "state UP") {
		return fmt.Errorf("interface %s is not UP", ir.InterfaceName)
	}

	// Check if IP is assigned
	cmd = exec.Command("ip", "addr", "show", ir.InterfaceName)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check IP assignment: %w", err)
	}

	if !strings.Contains(string(out), ir.BaseIP) {
		return fmt.Errorf("IP %s not assigned to interface %s", ir.BaseIP, ir.InterfaceName)
	}

	return nil
}

// SetupDNS configures DNS for the virtual interface.
func (ir *IPRange) SetupDNS(domain string, port int) error {
	logger.Log.Infof(
		"Configuring DNS for domain %s on virtual interface %s",
		domain,
		ir.InterfaceName,
	)

	cmds := [][]string{
		{"sudo", "resolvectl", "dns", ir.InterfaceName, fmt.Sprintf("%s:%d", ir.BaseIP, port)},
		{"sudo", "resolvectl", "domain", ir.InterfaceName, "~" + domain},
	}

	for _, cmd := range cmds {
		logger.Log.Debugf("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("DNS command failed: %w, output: %s", err, string(out))
		}
		logger.Log.Debugf("DNS command output: %s", string(out))
	}

	logger.Log.Infof(
		"DNS configuration applied on virtual interface %s for domain ~%s",
		ir.InterfaceName,
		domain,
	)
	return nil
}

// AddPortForward adds a port forward from the virtual interface to a target.
func (ir *IPRange) AddPortForward(port int, target string) error {
	ir.mu.Lock()
	defer ir.mu.Unlock()

	if _, exists := ir.PortForwards[port]; exists {
		return fmt.Errorf("port %d already forwarded", port)
	}

	// Use iptables to set up port forwarding
	cmds := [][]string{
		{
			"sudo",
			"iptables",
			"-t",
			"nat",
			"-A",
			"OUTPUT",
			"-d",
			ir.BaseIP,
			"-p",
			"tcp",
			"--dport",
			strconv.Itoa(port),
			"-j",
			"DNAT",
			"--to-destination",
			target,
		},
		{
			"sudo",
			"iptables",
			"-t",
			"nat",
			"-A",
			"POSTROUTING",
			"-d",
			strings.Split(target, ":")[0],
			"-p",
			"tcp",
			"--dport",
			strings.Split(target, ":")[1],
			"-j",
			"MASQUERADE",
		},
	}

	for _, cmd := range cmds {
		logger.Log.Debugf("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("iptables command failed: %w, output: %s", err, string(out))
		}
		logger.Log.Debugf("iptables output: %s", string(out))
	}

	ir.PortForwards[port] = target
	ir.LastUsed = time.Now()
	logger.Log.Infof("Port forward added: %s:%d -> %s", ir.BaseIP, port, target)
	return nil
}

// RemovePortForward removes a port forward.
func (ir *IPRange) RemovePortForward(port int) error {
	ir.mu.Lock()
	defer ir.mu.Unlock()

	target, exists := ir.PortForwards[port]
	if !exists {
		return fmt.Errorf("port %d not forwarded", port)
	}

	// Remove iptables rules
	cmds := [][]string{
		{
			"sudo",
			"iptables",
			"-t",
			"nat",
			"-D",
			"OUTPUT",
			"-d",
			ir.BaseIP,
			"-p",
			"tcp",
			"--dport",
			strconv.Itoa(port),
			"-j",
			"DNAT",
			"--to-destination",
			target,
		},
		{
			"sudo",
			"iptables",
			"-t",
			"nat",
			"-D",
			"POSTROUTING",
			"-d",
			strings.Split(target, ":")[0],
			"-p",
			"tcp",
			"--dport",
			strings.Split(target, ":")[1],
			"-j",
			"MASQUERADE",
		},
	}

	for _, cmd := range cmds {
		logger.Log.Debugf("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			logger.Log.Warnf("Failed to remove iptables rule: %v, output: %s", err, string(out))
		}
	}

	delete(ir.PortForwards, port)
	logger.Log.Infof("Port forward removed: %s:%d -> %s", ir.BaseIP, port, target)
	return nil
}

// stopPortForward is a helper function to stop port forwarding.
func (irm *IPRangeManager) stopPortForward(ipRange *IPRange, port int) error {
	return ipRange.RemovePortForward(port)
}

// DestroyVirtualInterface destroys the virtual interface and cleans up.
func (ir *IPRange) DestroyVirtualInterface() error {
	logger.Log.Infof("Destroying virtual interface %s", ir.InterfaceName)

	// Clean up all port forwards first
	ir.mu.Lock()
	for port := range ir.PortForwards {
		if err := ir.RemovePortForward(port); err != nil {
			logger.Log.Warnf("Failed to remove port forward on port %d: %v", port, err)
		}
	}
	ir.mu.Unlock()

	// Revert DNS configuration
	cmd := exec.Command("sudo", "resolvectl", "revert", ir.InterfaceName)
	logger.Log.Debugf("Reverting DNS: %s", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Warnf("Failed to revert DNS configuration: %v, output: %s", err, string(out))
	}

	// Remove virtual interface
	cmd = exec.Command("sudo", "ip", "link", "delete", ir.InterfaceName)
	logger.Log.Debugf("Removing interface: %s", strings.Join(cmd.Args, " "))
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove interface: %w, output: %s", err, string(out))
	}

	logger.Log.Infof("Virtual interface %s destroyed successfully", ir.InterfaceName)
	return nil
}

// GetPortForwards returns a copy of current port forwards.
func (ir *IPRange) GetPortForwards() map[int]string {
	ir.mu.RLock()
	defer ir.mu.RUnlock()

	forwards := make(map[int]string)
	for port, target := range ir.PortForwards {
		forwards[port] = target
	}
	return forwards
}

// GetStats returns statistics about the IP range.
func (ir *IPRange) GetStats() map[string]interface{} {
	ir.mu.RLock()
	defer ir.mu.RUnlock()

	return map[string]interface{}{
		"base_ip":        ir.BaseIP,
		"interface_name": ir.InterfaceName,
		"created":        ir.Created,
		"last_used":      ir.LastUsed,
		"port_forwards":  len(ir.PortForwards),
		"allocated":      ir.Allocated,
	}
}

// ListAllocatedRanges returns information about all allocated IP ranges.
func (irm *IPRangeManager) ListAllocatedRanges() map[string]*IPRange {
	irm.mu.RLock()
	defer irm.mu.RUnlock()

	ranges := make(map[string]*IPRange)
	for ip, ipRange := range irm.allocatedRanges {
		ranges[ip] = ipRange
	}
	return ranges
}

// CleanupAll cleans up all allocated IP ranges.
func (irm *IPRangeManager) CleanupAll() error {
	irm.mu.Lock()
	defer irm.mu.Unlock()

	var errors []error
	for baseIP, ipRange := range irm.allocatedRanges {
		if err := ipRange.DestroyVirtualInterface(); err != nil {
			errors = append(
				errors,
				fmt.Errorf("failed to destroy interface for %s: %w", baseIP, err),
			)
		}
	}

	// Clear all allocations
	irm.allocatedRanges = make(map[string]*IPRange)

	if len(errors) > 0 {
		return fmt.Errorf("cleanup completed with %d errors: %v", len(errors), errors)
	}

	logger.Log.Info("All IP ranges cleaned up successfully")
	return nil
}
