package dns

import (
	"errors"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

var (
	ErrDNSNotConfigured = errors.New("DNS not configured")
)

// SetupDNS configures systemd-resolved to redirect domain to our local DNS.
// It uses the virtual interface if available, otherwise skips DNS configuration.
func SetupDNS(domain string, port int, cfg *config.Config) (*VirtualInterface, error) {
	if !cfg.Network.UseVirtualInterface {
		logger.Log.Info("Virtual interface disabled, skipping DNS configuration")
		return nil, ErrDNSNotConfigured
	}

	// Create and configure virtual interface
	virtualInterface := NewVirtualInterface(cfg)
	if err := virtualInterface.Create(); err != nil {
		logger.Log.Warnf("Failed to create virtual interface: %v", err)
		logger.Log.Info("Skipping DNS configuration - virtual interface creation failed")
		return nil, ErrDNSNotConfigured
	}

	// Setup DNS on the virtual interface
	if err := virtualInterface.SetupDNS(domain, port); err != nil {
		logger.Log.Warnf("Failed to setup DNS on virtual interface: %v", err)
		logger.Log.Info("Skipping DNS configuration - DNS setup failed")
		return nil, ErrDNSNotConfigured
	}

	logger.Log.Infof(
		"DNS configured successfully on virtual interface %s",
		virtualInterface.GetName(),
	)
	return virtualInterface, nil
}

// RevertDNS reverts DNS configurations on the virtual interface.
func RevertDNS() error {
	logger.Log.Debug("Reverting DNS configuration")

	// The virtual interface cleanup is now handled directly by the ProxyDNS.Stop() method
	// This function is kept for compatibility but most cleanup is done elsewhere

	logger.Log.Debug("DNS configuration revert completed")
	return nil
}
