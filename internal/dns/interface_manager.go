package dns

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// InterfaceManager manages multiple virtual interfaces for different purposes.
type InterfaceManager struct {
	config       *config.Config
	dnsInterface *VirtualInterface
	pfInterface  *VirtualInterface // Port-forward interface
	dnsCreated   bool
	pfCreated    bool
}

// NewInterfaceManager creates a new interface manager.
func NewInterfaceManager(cfg *config.Config) *InterfaceManager {
	mgr := &InterfaceManager{
		config: cfg,
	}

	// Create DNS interface (main virtual interface for DNS resolution)
	mgr.dnsInterface = NewVirtualInterface(cfg)

	// Always create port-forward interface when virtual interfaces are enabled
	if cfg.Network.UseVirtualInterface {
		mgr.pfInterface = mgr.createPortForwardInterface(cfg)
	}

	return mgr
}

// createPortForwardInterface creates a separate virtual interface for port-forwarding.
func (mgr *InterfaceManager) createPortForwardInterface(cfg *config.Config) *VirtualInterface {
	// Create a temporary config for the port-forward interface
	pfConfig := *cfg // Copy the config

	// Override network settings for port-forward interface
	pfConfig.Network.VirtualInterfaceName = cfg.Network.PortForwardInterfaceName
	if pfConfig.Network.VirtualInterfaceName == "" {
		pfConfig.Network.VirtualInterfaceName = "kube-proxy0" // Default name for port-forward interface
	}

	pfConfig.Network.VirtualInterfaceIP = cfg.Network.PortForwardInterfaceIP
	if pfConfig.Network.VirtualInterfaceIP == "" {
		// Find a different IP from the DNS interface
		dnsIP := cfg.Network.VirtualInterfaceIP
		if dnsIP == "" {
			dnsIP = "10.8.0.1" // Default DNS IP
		}

		// Calculate a different IP for port-forward interface
		// If DNS uses 10.8.0.1, use 10.8.0.3 (skip .2 to avoid conflicts)
		// If DNS uses 10.8.0.2, use 10.8.0.4, etc.
		pfConfig.Network.VirtualInterfaceIP = mgr.findAlternateIP(dnsIP)
	}

	logger.Log.Infof("Creating port-forward interface: %s with IP %s",
		pfConfig.Network.VirtualInterfaceName,
		pfConfig.Network.VirtualInterfaceIP)

	return NewVirtualInterface(&pfConfig)
}

// findAlternateIP finds an alternate IP address for the port-forward interface.
func (mgr *InterfaceManager) findAlternateIP(dnsIP string) string {
	// Parse the DNS IP to get the base network
	parts := strings.Split(dnsIP, ".")
	if len(parts) != 4 {
		return "10.8.0.3" // Fallback default
	}

	// Try to increment the last octet by 2 to avoid conflicts
	lastOctet := 1
	if parts[3] != "" {
		if val, err := strconv.Atoi(parts[3]); err == nil {
			lastOctet = val + 2
		}
	}

	// Ensure we stay within valid range
	if lastOctet > 254 {
		lastOctet = 3 // Fallback
	}

	alternateIP := fmt.Sprintf("%s.%s.%s.%d", parts[0], parts[1], parts[2], lastOctet)
	logger.Log.Debugf("Port-forward interface will use IP: %s (DNS IP: %s)", alternateIP, dnsIP)

	return alternateIP
}

// CreateInterfaces creates all required virtual interfaces.
func (mgr *InterfaceManager) CreateInterfaces() error {
	// Always create DNS interface
	if mgr.config.Network.UseVirtualInterface {
		logger.Log.Info("Creating DNS virtual interface...")
		if err := mgr.dnsInterface.Create(); err != nil {
			return fmt.Errorf("failed to create DNS interface: %w", err)
		}
		mgr.dnsCreated = true
	}

	// Create port-forward interface when virtual interfaces are enabled
	if mgr.config.Network.UseVirtualInterface && mgr.pfInterface != nil {
		logger.Log.Info("Creating port-forward virtual interface...")
		if err := mgr.pfInterface.Create(); err != nil {
			// Don't fail completely if port-forward interface creation fails
			logger.Log.Warnf("Failed to create port-forward interface: %v", err)
			logger.Log.Info("Continuing with DNS interface only")
		} else {
			mgr.pfCreated = true
		}
	}

	return nil
}

// GetDNSInterface returns the DNS virtual interface.
func (mgr *InterfaceManager) GetDNSInterface() *VirtualInterface {
	return mgr.dnsInterface
}

// GetPortForwardInterface returns the port-forward virtual interface.
func (mgr *InterfaceManager) GetPortForwardInterface() *VirtualInterface {
	return mgr.pfInterface
}

// GetPortForwardIP returns the IP address to use for port forwarding.
func (mgr *InterfaceManager) GetPortForwardIP() string {
	// If port-forward interface is created and available, use its IP
	if mgr.pfCreated && mgr.pfInterface != nil {
		if ip := mgr.pfInterface.GetIP(); ip != "" {
			logger.Log.Debugf("Using port-forward interface IP: %s", ip)
			return ip
		}
	}

	// Fall back to explicit port-forward bind IP
	if mgr.config.Network.PortForwardBindIP != "" {
		logger.Log.Debugf(
			"Using configured port-forward bind IP: %s",
			mgr.config.Network.PortForwardBindIP,
		)
		return mgr.config.Network.PortForwardBindIP
	}

	// Fall back to DNS interface IP
	if mgr.dnsCreated && mgr.dnsInterface != nil {
		if ip := mgr.dnsInterface.GetIP(); ip != "" {
			logger.Log.Debugf("Using DNS interface IP for port forwarding: %s", ip)
			return ip
		}
	}

	// Final fallback to localhost
	logger.Log.Debug("Using localhost for port forwarding: 127.0.0.1")
	return "127.0.0.1"
}

// IsDNSInterfaceCreated returns true if the DNS interface was successfully created.
func (mgr *InterfaceManager) IsDNSInterfaceCreated() bool {
	return mgr.dnsCreated
}

// IsPortForwardInterfaceCreated returns true if the port-forward interface was successfully created.
func (mgr *InterfaceManager) IsPortForwardInterfaceCreated() bool {
	return mgr.pfCreated
}

// Cleanup cleans up all created interfaces.
func (mgr *InterfaceManager) Cleanup() error {
	var lastErr error

	// Cleanup port-forward interface first
	if mgr.pfCreated && mgr.pfInterface != nil {
		logger.Log.Info("Cleaning up port-forward interface...")
		if err := mgr.pfInterface.Cleanup(); err != nil {
			logger.Log.Warnf("Failed to cleanup port-forward interface: %v", err)
			lastErr = err
		}
		mgr.pfCreated = false
	}

	// Cleanup DNS interface
	if mgr.dnsCreated && mgr.dnsInterface != nil {
		logger.Log.Info("Cleaning up DNS interface...")
		if err := mgr.dnsInterface.Cleanup(); err != nil {
			logger.Log.Warnf("Failed to cleanup DNS interface: %v", err)
			lastErr = err
		}
		mgr.dnsCreated = false
	}

	return lastErr
}
