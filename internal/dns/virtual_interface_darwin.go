//go:build darwin
// +build darwin

package dns

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// VirtualInterface manages a loopback alias on macOS for cluster DNS.
type VirtualInterface struct {
	name       string
	ip         string
	subnet     string
	created    bool
	configured bool
	config     *config.Config
}

// NewVirtualInterface creates a new virtual interface manager for macOS.
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

// Create creates the virtual interface and configures it on macOS.
func (vi *VirtualInterface) Create() error {
	logger.Log.Infof("Creating virtual interface alias on lo0 for cluster DNS")

	// On macOS, we create an alias on lo0 (loopback interface)
	if err := vi.createLoopbackAlias(); err != nil {
		return fmt.Errorf("failed to create loopback alias: %w", err)
	}

	vi.created = true
	vi.configured = true

	logger.Log.Infof("✅ Virtual interface created successfully: %s with IP %s", vi.name, vi.ip)
	return nil
}

// createLoopbackAlias creates an alias on the loopback interface.
func (vi *VirtualInterface) createLoopbackAlias() error {
	// Check if the IP is already configured
	if vi.isIPConfigured() {
		logger.Log.Infof("IP %s already configured on lo0", vi.ip)
		return nil
	}

	// Create alias: sudo ifconfig lo0 alias <ip>
	cmd := exec.Command("sudo", "ifconfig", "lo0", "alias", vi.ip)
	logger.Log.Infof("Creating loopback alias: %s", strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create loopback alias: %w, output: %s", err, string(out))
	}

	logger.Log.Infof("Loopback alias created for IP %s", vi.ip)
	return nil
}

// isIPConfigured checks if the IP is already configured on lo0.
func (vi *VirtualInterface) isIPConfigured() bool {
	cmd := exec.Command("ifconfig", "lo0")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.Contains(string(out), vi.ip)
}

// interfaceExists checks if the interface exists (for macOS, we always use lo0).
func (vi *VirtualInterface) interfaceExists() bool {
	cmd := exec.Command("ifconfig", "lo0")
	return cmd.Run() == nil
}

// SetupDNS configures DNS resolution using macOS /etc/resolver/ mechanism.
func (vi *VirtualInterface) SetupDNS(domain string, port int) error {
	if !vi.created {
		return errors.New("virtual interface not created")
	}

	logger.Log.Infof("Configuring macOS DNS resolver for domain: %s", domain)

	// Create /etc/resolver directory if it doesn't exist
	resolverDir := "/etc/resolver"
	if err := vi.ensureResolverDirectory(resolverDir); err != nil {
		return fmt.Errorf("failed to create resolver directory: %w", err)
	}

	// Create resolver configuration file for the domain
	resolverFile := fmt.Sprintf("%s/%s", resolverDir, domain)
	resolverConfig := fmt.Sprintf("nameserver %s\nport %d\n", vi.ip, port)

	// Write resolver configuration
	if err := vi.writeResolverConfig(resolverFile, resolverConfig); err != nil {
		return fmt.Errorf("failed to write resolver config: %w", err)
	}

	logger.Log.Infof("DNS resolver configured: %s -> %s:%d", domain, vi.ip, port)
	vi.configured = true

	return nil
}

// ensureResolverDirectory ensures /etc/resolver directory exists.
func (vi *VirtualInterface) ensureResolverDirectory(dir string) error {
	// Check if directory exists
	cmd := exec.Command("test", "-d", dir)
	if err := cmd.Run(); err != nil {
		// Directory doesn't exist, create it
		logger.Log.Debugf("Creating resolver directory: %s", dir)
		cmd = exec.Command("sudo", "mkdir", "-p", dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("mkdir failed: %v, output: %s", err, string(out))
		}
		logger.Log.Debugf("Resolver directory created: %s", dir)
	}
	return nil
}

// writeResolverConfig writes the resolver configuration file.
func (vi *VirtualInterface) writeResolverConfig(filePath, content string) error {
	// Use tee to write with sudo privileges
	// echo content | sudo tee file > /dev/null
	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(content)

	logger.Log.Debugf("Writing resolver config to: %s", filePath)
	logger.Log.Debugf("Resolver config content:\n%s", content)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tee command failed: %v, output: %s", err, string(out))
	}

	logger.Log.Debugf("Resolver configuration written to: %s", filePath)
	return nil
}

// Cleanup removes the virtual interface alias and resolver configuration.
func (vi *VirtualInterface) Cleanup() error {
	if !vi.created {
		logger.Log.Debug("Virtual interface not created, skipping cleanup")
		return nil
	}

	logger.Log.Infof("Cleaning up virtual interface alias for IP %s", vi.ip)

	// Remove resolver configuration file if it was configured
	if vi.configured {
		// Extract domain from interface name (e.g., kube-dns0 -> svc.cluster.local)
		// For now, we'll use a fixed domain name since that's what we're configuring
		resolverFile := "/etc/resolver/svc.cluster.local"
		if err := vi.removeResolverConfig(resolverFile); err != nil {
			logger.Log.Warnf("Failed to remove resolver config: %v", err)
			// Don't return error, continue cleanup
		}
	}

	// Remove alias: sudo ifconfig lo0 -alias <ip>
	cmd := exec.Command("sudo", "ifconfig", "lo0", "-alias", vi.ip)
	logger.Log.Debugf("Removing loopback alias: %s", strings.Join(cmd.Args, " "))

	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Warnf("Failed to remove loopback alias: %v, output: %s", err, string(out))
		// Don't return error, cleanup should continue
	} else {
		logger.Log.Infof("Loopback alias removed for IP %s", vi.ip)
	}

	vi.created = false
	vi.configured = false

	return nil
}

// removeResolverConfig removes the resolver configuration file.
func (vi *VirtualInterface) removeResolverConfig(filePath string) error {
	// Check if file exists first
	cmd := exec.Command("test", "-f", filePath)
	if err := cmd.Run(); err != nil {
		logger.Log.Debugf("Resolver config file doesn't exist: %s", filePath)
		return nil // Not an error if file doesn't exist
	}

	logger.Log.Debugf("Removing resolver config: %s", filePath)
	cmd = exec.Command("sudo", "rm", "-f", filePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rm command failed: %v, output: %s", err, string(out))
	}

	logger.Log.Infof("Resolver configuration removed: %s", filePath)
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

// IsCreated returns whether the virtual interface has been created.
func (vi *VirtualInterface) IsCreated() bool {
	return vi.created
}
