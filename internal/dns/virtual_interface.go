package dns

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	// DefaultVirtualInterfaceName is the default name of the DNS virtual interface.
	DefaultVirtualInterfaceName = "kube-dns0"

	dummyInterfaceType = "dummy"
)

// VirtualInterface manages a dummy network interface for cluster DNS.
type VirtualInterface struct {
	name       string
	ip         string
	subnet     string
	created    bool
	configured bool
	config     *config.Config
}

// NewVirtualInterface creates a new virtual interface manager.
func NewVirtualInterface(cfg *config.Config) *VirtualInterface {
	name := cfg.Network.VirtualInterfaceName
	if name == "" {
		name = DefaultVirtualInterfaceName
	}

	ip := cfg.Network.VirtualInterfaceIP
	if ip == "" {
		ip = "10.8.0.1" // Default IP if none specified
		logger.Log.Debugf("No IP configured, using default: %s", ip)
	} else {
		logger.Log.Debugf("Using configured IP: %s", ip)
	}

	// If the configured IP is already in use, find a free one
	logger.Log.Debugf("Checking if IP %s is already in use...", ip)
	if isIPInUse(ip) {
		logger.Log.Infof("Configured IP %s is already in use, finding free IP...", ip)
		freeIP := findFreeIP()
		if freeIP != "" {
			logger.Log.Infof("Using free IP: %s", freeIP)
			ip = freeIP
		} else {
			logger.Log.Warnf("Could not find free IP, using configured IP: %s", ip)
		}
	} else {
		logger.Log.Debugf("IP %s is available for use", ip)
	}

	subnet := calculateSubnet(ip)

	vi := &VirtualInterface{
		name:   name,
		ip:     ip,
		subnet: subnet,
		config: cfg,
	}

	logger.Log.Debugf("Virtual interface configuration: name=%s, ip=%s, subnet=%s",
		vi.name, vi.ip, vi.subnet)
	return vi
}

// calculateSubnet calculates the subnet from an IP address.
func calculateSubnet(ip string) string {
	if strings.HasPrefix(ip, "127.") {
		return ip + "/32" // Use single IP for loopback
	}
	// Use /24 for other ranges
	return ip[:strings.LastIndex(ip, ".")] + ".0/24"
}

// Create creates the virtual interface and configures it.
func (vi *VirtualInterface) Create() error {
	logger.Log.Infof("Creating virtual interface %s for cluster DNS", vi.name)

	// Check if interface already exists
	if vi.interfaceExists() {
		return vi.handleExistingInterface()
	}

	// Create new interface
	return vi.createInterface()
}

// handleExistingInterface handles the case when an interface already exists.
func (vi *VirtualInterface) handleExistingInterface() error {
	logger.Log.Infof("Virtual interface %s already exists, checking configuration", vi.name)

	// Check if the existing interface is in a good state
	if vi.isInterfaceHealthy() {
		vi.created = true
		return vi.verifyAndReconfigureIfNeeded()
	}

	// Interface is in bad state, remove and recreate
	logger.Log.Infof("Existing interface %s is in bad state, removing and recreating", vi.name)
	if cleanupErr := vi.cleanup(); cleanupErr != nil {
		logger.Log.Warnf("Failed to cleanup interface: %v", cleanupErr)
	}
	return vi.createInterface()
}

// verifyAndReconfigureIfNeeded verifies the existing interface and reconfigures if necessary.
func (vi *VirtualInterface) verifyAndReconfigureIfNeeded() error {
	// Verify existing configuration
	if err := vi.verifyInterface(); err != nil {
		logger.Log.Infof("Existing interface needs reconfiguration: %v", err)
		return vi.reconfigureOrRecreate()
	}

	logger.Log.Infof("Existing virtual interface is properly configured")
	return nil
}

// reconfigureOrRecreate attempts to reconfigure the interface or recreates it if that fails.
func (vi *VirtualInterface) reconfigureOrRecreate() error {
	// Try to reconfigure existing interface
	if err := vi.reconfigureExistingInterface(); err != nil {
		logger.Log.Warnf("Failed to reconfigure existing interface: %v", err)
		// Remove and recreate
		if cleanupErr := vi.cleanup(); cleanupErr != nil {
			logger.Log.Warnf(
				"Failed to cleanup interface during recreation: %v",
				cleanupErr,
			)
		}
		return vi.createInterface()
	}
	return nil
}

// createInterface creates the dummy interface for DNS resolution.
func (vi *VirtualInterface) createInterface() error {
	logger.Log.Debugf("Creating virtual interface %s with IP %s", vi.name, vi.ip)

	// Validate IP address is set
	if vi.ip == "" {
		return errors.New("virtual interface IP address is not configured")
	}

	// Validate interface name to prevent injection
	if err := vi.validateInterfaceName(); err != nil {
		return err
	}

	// Create dummy interface for DNS resolution
	if err := vi.createDummyInterface(); err != nil {
		return err
	}

	// Configure IP address
	if err := vi.configureIP(); err != nil {
		vi.cleanupOnError("IP config failure")
		return fmt.Errorf("failed to configure IP: %w", err)
	}

	// Bring interface up
	if err := vi.bringUp(); err != nil {
		vi.cleanupOnError("bring up failure")
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	vi.created = true
	logger.Log.Infof("Virtual interface %s created successfully with IP %s", vi.name, vi.ip)
	return nil
}

// validateInterfaceName validates the interface name to prevent injection.
func (vi *VirtualInterface) validateInterfaceName() error {
	validName := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validName.MatchString(vi.name) {
		return fmt.Errorf("invalid interface name: %s", vi.name)
	}
	return nil
}

// validateAndGetName validates the interface name and returns it if valid.
func (vi *VirtualInterface) validateAndGetName() (string, error) {
	if err := vi.validateInterfaceName(); err != nil {
		return "", err
	}
	return vi.name, nil
}

// createDummyInterface creates the dummy interface.
func (vi *VirtualInterface) createDummyInterface() error {
	// Create dummy interface
	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", "ip", "link", "add", name, "type", "dummy")
	logger.Log.Infof("Creating %s interface: %s", dummyInterfaceType, strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Check if interface already exists
		if strings.Contains(string(out), "RTNETLINK answers: File exists") {
			logger.Log.Debugf("Interface %s already exists, continuing", vi.name)
		} else {
			return fmt.Errorf("failed to create %s interface: %w, output: %s", dummyInterfaceType, err, string(out))
		}
	}
	return nil
}

// cleanupOnError performs cleanup when an error occurs during interface creation.
func (vi *VirtualInterface) cleanupOnError(context string) {
	if cleanupErr := vi.cleanup(); cleanupErr != nil {
		logger.Log.Warnf("Failed to cleanup interface after %s: %v", context, cleanupErr)
	}
}

// reconfigureExistingInterface attempts to reconfigure an existing interface.
func (vi *VirtualInterface) reconfigureExistingInterface() error {
	logger.Log.Debugf("Reconfiguring existing interface %s", vi.name)

	// Configure IP address (this will flush existing IPs first)
	if err := vi.configureIP(); err != nil {
		return fmt.Errorf("failed to configure IP: %w", err)
	}

	// Ensure interface is up
	if err := vi.bringUp(); err != nil {
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	return nil
}

// SetupDNS configures DNS resolution to use the virtual interface for cluster domains.
func (vi *VirtualInterface) SetupDNS(domain string, port int) error {
	if !vi.created {
		return errors.New("virtual interface not created")
	}

	logger.Log.Infof("Configuring DNS for domain %s on virtual interface %s", domain, vi.name)

	// Validate interface name to prevent injection
	validName := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validName.MatchString(vi.name) {
		return fmt.Errorf("invalid interface name: %s", vi.name)
	}

	cmds := [][]string{
		{"sudo", "-v"},
		{"sudo", "resolvectl", "dns", vi.name, fmt.Sprintf("%s:%d", vi.ip, port)},
		{"sudo", "resolvectl", "domain", vi.name, "~" + domain},
	}

	for _, cmd := range cmds {
		logger.Log.Infof("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("command failed: %w, output: %s", err, string(out))
		}
		logger.Log.Debugf("Command output: %s", string(out))
	}

	vi.configured = true
	logger.Log.Infof(
		"DNS configuration applied on virtual interface %s for domain ~%s",
		vi.name,
		domain,
	)
	return nil
}

// Cleanup removes the virtual interface and DNS configuration.
func (vi *VirtualInterface) Cleanup() error {
	var errors []string

	// Revert DNS configuration
	if vi.configured {
		if err := vi.revertDNS(); err != nil {
			errors = append(errors, fmt.Sprintf("DNS revert failed: %v", err))
		}
	}

	// Remove virtual interface
	if vi.created {
		if err := vi.cleanup(); err != nil {
			errors = append(errors, fmt.Sprintf("interface cleanup failed: %v", err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, "; "))
	}

	logger.Log.Infof("Virtual interface %s cleaned up successfully", vi.name)
	return nil
}

// GetIP returns the IP address of the virtual interface.
func (vi *VirtualInterface) GetIP() string {
	return vi.ip
}

// GetName returns the name of the virtual interface.
func (vi *VirtualInterface) GetName() string {
	return vi.name
}

// interfaceExists checks if the interface already exists.
func (vi *VirtualInterface) interfaceExists() bool {
	cmd := exec.Command("ip", "link", "show", vi.name)
	err := cmd.Run()
	exists := err == nil
	logger.Log.Debugf("Interface %s exists: %v", vi.name, exists)
	return exists
}

// isInterfaceHealthy checks if an existing interface is in a good state.
func (vi *VirtualInterface) isInterfaceHealthy() bool {
	// Check if interface is up and has carrier
	cmd := exec.Command("ip", "link", "show", vi.name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to check interface health: %v", err)
		return false
	}

	output := string(out)

	// For dummy interfaces, check for UP and LOWER_UP flags
	// Dummy interfaces show as "UNKNOWN" state but have UP and LOWER_UP flags
	if !strings.Contains(output, "UP") || !strings.Contains(output, "LOWER_UP") {
		logger.Log.Debugf("Interface %s is not UP or LOWER_UP", vi.name)
		return false
	}

	// Check if interface has IP address
	name, err := vi.validateAndGetName()
	if err != nil {
		return false
	}
	cmd = exec.Command("ip", "addr", "show", name)
	out, err = cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to check interface health: %v", err)
		return false
	}

	// Check if the interface has the expected IP
	if !strings.Contains(string(out), vi.ip) {
		logger.Log.Debugf("Interface %s does not have expected IP %s", name, vi.ip)
		return false
	}

	logger.Log.Debugf("Interface %s is healthy", vi.name)
	return true
}

// configureIP assigns an IP address to the interface.
func (vi *VirtualInterface) configureIP() error {
	// Check if IP is already in use on any interface
	if isIPInUse(vi.ip) {
		return fmt.Errorf("IP address %s is already in use on another interface", vi.ip)
	}

	// First, try to remove any existing IP configurations to avoid conflicts
	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", "ip", "addr", "flush", "dev", name)
	logger.Log.Debugf("Flushing existing IPs: %s", strings.Join(cmd.Args, " "))
	flushOut, flushErr := cmd.CombinedOutput() // Ignore errors as the interface might be new
	if flushErr != nil {
		logger.Log.Debugf(
			"Flush command failed (this may be normal for new interfaces): %v",
			flushErr,
		)
	}
	logger.Log.Debugf("Flush output: %s", string(flushOut))

	// Use appropriate configuration based on IP type
	var configCmd *exec.Cmd
	if strings.HasPrefix(vi.ip, "127.") {
		// For loopback IPs, use single IP without subnet
		configCmd = exec.Command("sudo", "ip", "addr", "add", vi.ip+"/32", "dev", name)
	} else {
		// For other IPs, use the specific IP with /24 subnet
		configCmd = exec.Command("sudo", "ip", "addr", "add", vi.ip+"/24", "dev", name)
	}

	logger.Log.Infof("Configuring IP: %s", strings.Join(configCmd.Args, " "))

	out, err := configCmd.CombinedOutput()
	if err != nil {
		// Check if address already exists
		if strings.Contains(string(out), "RTNETLINK answers: File exists") {
			logger.Log.Debugf("IP address already configured on %s", vi.name)
		} else {
			logger.Log.Debugf("IP configuration failed: %s", string(out))
			return fmt.Errorf("failed to configure IP: %w, output: %s", err, string(out))
		}
	}

	// Wait a moment for the IP to be fully configured
	time.Sleep(200 * time.Millisecond)

	// Verify the IP was assigned
	if err := vi.verifyIPAssigned(); err != nil {
		return fmt.Errorf("IP assignment verification failed: %w", err)
	}

	logger.Log.Debugf("IP configured successfully: %s", string(out))
	return nil
}

// bringUp brings the interface up.
func (vi *VirtualInterface) bringUp() error {
	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", "ip", "link", "set", name, "up")
	logger.Log.Infof("Bringing interface up: %s", strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring interface up: %w, output: %s", err, string(out))
	}

	// Wait for the interface to be fully ready (dummy interfaces are very fast)
	time.Sleep(200 * time.Millisecond)

	// Verify the interface is accessible
	logger.Log.Debugf("Verifying interface accessibility...")
	if err := vi.verifyInterfaceWithRetries(); err != nil {
		logger.Log.Warnf("Interface verification failed: %v", err)
		logger.Log.Infof(
			"Interface created but verification failed - this may be normal on some systems",
		)
		// Continue without failing - interface might still work
		logger.Log.Infof("Continuing with interface setup despite verification failure")
	} else {
		logger.Log.Infof("Interface verification successful")
	}

	logger.Log.Debugf("Interface brought up: %s", string(out))
	return nil
}

// verifyInterface verifies that the interface is properly configured.
func (vi *VirtualInterface) verifyInterface() error {
	// Check if the interface exists and has the IP
	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("ip", "addr", "show", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("interface not found: %w", err)
	}

	logger.Log.Debugf("Interface %s address info: %s", vi.name, string(out))

	// Check if the IP is assigned
	if !strings.Contains(string(out), vi.ip) {
		return fmt.Errorf("IP %s not found on interface %s", vi.ip, vi.name)
	}

	// Check if interface is up using ip link show
	// For dummy interfaces, check for UP and LOWER_UP flags instead of "state UP"
	cmd = exec.Command("ip", "link", "show", name)
	linkOut, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check interface link status: %w", err)
	}

	logger.Log.Debugf("Interface %s link info: %s", vi.name, string(linkOut))

	// For dummy interfaces, check for UP and LOWER_UP flags
	// Dummy interfaces show as "UNKNOWN" state but have UP and LOWER_UP flags
	if !strings.Contains(string(linkOut), "UP") || !strings.Contains(string(linkOut), "LOWER_UP") {
		return fmt.Errorf("interface %s is not UP or LOWER_UP", vi.name)
	}

	logger.Log.Debugf("Virtual interface %s verification successful", vi.name)
	return nil
}

// verifyInterfaceWithRetries verifies the interface with multiple attempts.
func (vi *VirtualInterface) verifyInterfaceWithRetries() error {
	maxRetries := 5
	baseDelay := 200 * time.Millisecond

	for attempt := range maxRetries {
		if err := vi.verifyInterface(); err == nil {
			// Basic interface verification passed
			logger.Log.Debugf("Interface verification passed on attempt %d", attempt+1)
			return nil
		}

		if attempt < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<attempt) // Exponential backoff
			logger.Log.Debugf(
				"Interface verification attempt %d failed, retrying in %v",
				attempt+1,
				delay,
			)
			time.Sleep(delay)
		}
	}

	// Final attempt without binding check
	// If all retries failed, still return success if basic interface verification passes
	if err := vi.verifyInterface(); err != nil {
		return fmt.Errorf("interface verification failed after %d attempts: %w", maxRetries, err)
	}

	logger.Log.Debugf("Interface exists and is configured, but binding may not work")
	return nil
}

// isIPInUse checks if the IP address is already in use on any interface.
func isIPInUse(ip string) bool {
	// Check if IP is set
	if ip == "" {
		logger.Log.Debugf("IP address not set, skipping IP usage check")
		return false
	}

	cmd := exec.Command("ip", "addr", "show")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to check IP usage: %v", err)
		return false
	}

	inUse := strings.Contains(string(out), ip+"/")
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
	cmd := exec.Command("ip", "addr", "show")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debugf("Failed to get used IPs: %v", err)
		return []string{}
	}

	var usedIPs []string
	lines := strings.SplitSeq(string(out), "\n")
	for line := range lines {
		if strings.Contains(line, "inet ") && !strings.Contains(line, "inet6") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				ip := strings.Split(parts[1], "/")[0]
				usedIPs = append(usedIPs, ip)
			}
		}
	}

	return usedIPs
}

// isIPInList checks if an IP is in a list of used IPs.
func isIPInList(ip string, usedIPs []string) bool {
	return slices.Contains(usedIPs, ip)
}

// verifyIPAssigned checks that the IP was properly assigned to the interface.
func (vi *VirtualInterface) verifyIPAssigned() error {
	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("ip", "addr", "show", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check interface: %w", err)
	}

	if !strings.Contains(string(out), vi.ip) {
		return fmt.Errorf("IP %s not found on interface %s", vi.ip, vi.name)
	}

	logger.Log.Debugf("IP %s successfully assigned to %s", vi.ip, vi.name)
	return nil
}

// revertDNS reverts DNS configuration on the virtual interface.
func (vi *VirtualInterface) revertDNS() error {
	// Validate interface name to prevent injection
	validName := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validName.MatchString(vi.name) {
		return fmt.Errorf("invalid interface name: %s", vi.name)
	}

	cmd := exec.Command("sudo", "resolvectl", "revert", vi.name)
	logger.Log.Infof("Reverting DNS configuration: %s", strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Warnf("Failed to revert DNS configuration: %v, output: %s", err, string(out))
		return err
	}

	vi.configured = false
	logger.Log.Debugf("DNS configuration reverted: %s", string(out))
	return nil
}

// cleanup removes the virtual interface.
func (vi *VirtualInterface) cleanup() error {
	if !vi.interfaceExists() {
		logger.Log.Debugf("Virtual interface %s does not exist, nothing to clean up", vi.name)
		return nil
	}

	name, err := vi.validateAndGetName()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", "ip", "link", "delete", name)
	logger.Log.Infof("Removing virtual interface: %s", strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove virtual interface: %w, output: %s", err, string(out))
	}

	vi.created = false
	logger.Log.Debugf("Virtual interface removed: %s", string(out))
	return nil
}
