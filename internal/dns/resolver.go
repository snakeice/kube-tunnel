package dns

import (
	"errors"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

var (
	ErrDNSNotConfigured = errors.New("DNS not configured")
)

// SetupDNS configures DNS resolution for a given domain using virtual interfaces.
// It creates the appropriate virtual interfaces and sets up DNS.
func SetupDNS(domain string, port int, cfg *config.Config) (*InterfaceManager, error) {
	if !cfg.Network.UseVirtualInterface {
		logger.Log.Info("Virtual interface disabled, skipping DNS configuration")
		return nil, ErrDNSNotConfigured
	}

	// Create interface manager to handle both DNS and port-forward interfaces
	interfaceManager := NewInterfaceManager(cfg)

	// Create all required interfaces
	if err := interfaceManager.CreateInterfaces(); err != nil {
		logger.Log.Warnf("Failed to create virtual interfaces: %v", err)
		logger.Log.Info("Skipping DNS configuration - interface creation failed")
		return nil, ErrDNSNotConfigured
	}

	// Setup DNS on the DNS virtual interface
	dnsInterface := interfaceManager.GetDNSInterface()
	if dnsInterface != nil && interfaceManager.IsDNSInterfaceCreated() {
		if err := dnsInterface.SetupDNS(domain, port); err != nil {
			logger.Log.Warnf("Failed to setup DNS on virtual interface: %v", err)
			logger.Log.Info("Skipping DNS configuration - DNS setup failed")
			return nil, ErrDNSNotConfigured
		}

		logger.Log.Infof(
			"DNS configured successfully on virtual interface %s",
			dnsInterface.GetName(),
		)
	}

	// Log port-forward interface status
	if interfaceManager.IsPortForwardInterfaceCreated() {
		pfInterface := interfaceManager.GetPortForwardInterface()
		if pfInterface != nil {
			logger.Log.Infof(
				"Port-forward interface configured: %s with IP %s",
				pfInterface.GetName(),
				pfInterface.GetIP(),
			)
		}
	}

	return interfaceManager, nil
}

// RevertDNS reverts DNS configurations on the virtual interface.
func RevertDNS() error {
	logger.Log.Debug("Reverting DNS configuration")

	// The virtual interface cleanup is now handled directly by the ProxyDNS.Stop() method
	// This function is kept for compatibility but most cleanup is done elsewhere

	logger.Log.Debug("DNS configuration revert completed")
	return nil
}
