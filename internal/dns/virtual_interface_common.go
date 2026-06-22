package dns

import (
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	// DefaultVirtualInterfaceName is the default name of the DNS virtual interface.
	DefaultVirtualInterfaceName = "kube-dns0"

	dummyInterfaceType = "dummy"
)

// calculateSubnet calculates the subnet from an IP address.
func calculateSubnet(ip string) string {
	if strings.HasPrefix(ip, "127.") {
		return ip + "/32" // Use single IP for loopback
	}
	// Use /24 for other ranges
	return ip[:strings.LastIndex(ip, ".")] + ".0/24"
}

// isIPInUse checks if the IP address is already in use on any interface.
// This is a platform-agnostic version that works on both macOS and Linux.
func isIPInUse(ip string) bool {
	// Check if IP is set
	if ip == "" {
		logger.Log.Debugf("IP address not set, skipping IP usage check")
		return false
	}

	// Try platform-specific commands
	var cmd *exec.Cmd

	// Try 'ip' command first (Linux)
	if _, err := exec.LookPath("ip"); err == nil {
		cmd = exec.Command("ip", "addr", "show")
	} else if _, err := exec.LookPath("ifconfig"); err == nil {
		// Fall back to ifconfig (macOS, BSD)
		cmd = exec.Command("ifconfig")
	} else {
		logger.Log.Debugf("No network command found to check IP usage")
		return false
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to check IP usage: %v", err)
		return false
	}

	// Check for IP in output (with or without CIDR)
	inUse := strings.Contains(string(out), ip+"/") || strings.Contains(string(out), ip+" ")
	if inUse {
		logger.Log.Debugf("IP %s is already in use", ip)
	}
	return inUse
}

// findFreeIP finds an available IP address in common private ranges.
func findFreeIP() string {
	// Get custom IP ranges from configuration if available
	var ranges []string

	// Try to get configuration
	cfg := config.GetConfig()
	if cfg != nil && len(cfg.Network.CustomIPRanges) > 0 {
		ranges = cfg.Network.CustomIPRanges
		logger.Log.Debugf("Using custom IP ranges from configuration: %v", ranges)
	} else {
		// Fallback to default ranges
		ranges = []string{
			"10.8.0.0/24",      // 10.8.0.1 - 10.8.0.254 (kube-tunnel default)
			"10.9.0.0/24",      // 10.9.0.1 - 10.9.0.254
			"10.10.0.0/24",     // 10.10.0.1 - 10.10.0.254
			"10.11.0.0/24",     // 10.11.0.1 - 10.11.0.254
			"10.12.0.0/24",     // 10.12.0.1 - 10.12.0.254
			"172.16.0.0/24",    // 172.16.0.1 - 172.16.0.254
			"172.17.0.0/24",    // 172.17.0.1 - 172.17.0.254
			"172.18.0.0/24",    // 172.18.0.1 - 172.18.0.254
			"192.168.100.0/24", // 192.168.100.1 - 192.168.100.254
			"192.168.200.0/24", // 192.168.200.1 - 192.168.200.254
			"192.168.250.0/24", // 192.168.250.1 - 192.168.250.254
		}
		logger.Log.Debugf("Using default IP ranges: %v", ranges)
	}

	// Get all currently used IPs to avoid checking them individually
	usedIPs := getUsedIPs()

	for _, ipRange := range ranges {
		// Extract base IP from range
		baseIP := strings.Split(ipRange, "/")[0]
		parts := strings.Split(baseIP, ".")
		if len(parts) != 4 {
			continue
		}

		// Try IPs in this range (starting from .1, skip .255)
		for i := 1; i <= 254; i++ {
			testIP := fmt.Sprintf("%s.%s.%s.%d", parts[0], parts[1], parts[2], i)

			// Check if this IP is already in use
			if !isIPInList(testIP, usedIPs) {
				logger.Log.Debugf("Found free IP: %s", testIP)
				return testIP
			}
		}
	}

	logger.Log.Warnf("No free IP addresses found in any range")
	return ""
}

// getUsedIPs returns a list of all currently used IP addresses.
func getUsedIPs() []string {
	var cmd *exec.Cmd

	// Try 'ip' command first (Linux)
	if _, err := exec.LookPath("ip"); err == nil {
		cmd = exec.Command("ip", "addr", "show")
	} else if _, err := exec.LookPath("ifconfig"); err == nil {
		// Fall back to ifconfig (macOS, BSD)
		cmd = exec.Command("ifconfig")
	} else {
		logger.Log.Debugf("No network command found to get used IPs")
		return []string{}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to get used IPs: %v", err)
		return []string{}
	}

	return parseIPCommandOutput(string(out), cmd.Path)
}

func parseIPCommandOutput(out, path string) []string {
	if strings.Contains(path, "ifconfig") {
		return parseIfconfigOutput(out)
	}

	return parseIPAddrOutput(out)
}

func parseIfconfigOutput(out string) []string {
	re := regexp.MustCompile(`inet\s+(\d+\.\d+\.\d+\.\d+)`)
	matches := re.FindAllStringSubmatch(out, -1)

	usedIPs := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			usedIPs = append(usedIPs, match[1])
		}
	}

	return usedIPs
}

func parseIPAddrOutput(out string) []string {
	var usedIPs []string
	lines := strings.Split(out, "\n")

	for _, line := range lines {
		if !strings.Contains(line, "inet ") || strings.Contains(line, "inet6") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 2 {
			ip := strings.Split(parts[1], "/")[0]
			usedIPs = append(usedIPs, ip)
		}
	}

	return usedIPs
}

// isIPInList checks if an IP is in a list of used IPs.
func isIPInList(ip string, usedIPs []string) bool {
	return slices.Contains(usedIPs, ip)
}
