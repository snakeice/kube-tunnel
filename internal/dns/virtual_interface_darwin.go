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

// SetupDNS configures DNS resolution (stub for macOS - uses /etc/resolver/).
func (vi *VirtualInterface) SetupDNS(domain string, port int) error {
	if !vi.created {
		return errors.New("virtual interface not created")
	}

	logger.Log.Infof("DNS setup for macOS should use /etc/resolver/ mechanism")
	logger.Log.Infof("Create /etc/resolver/cluster.local with content:")
	logger.Log.Infof("  nameserver %s", vi.ip)
	logger.Log.Infof("  port %d", port)

	return nil
}

// Cleanup removes the virtual interface alias.
func (vi *VirtualInterface) Cleanup() error {
	if !vi.created {
		logger.Log.Debug("Virtual interface not created, skipping cleanup")
		return nil
	}

	logger.Log.Infof("Cleaning up virtual interface alias for IP %s", vi.ip)

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
