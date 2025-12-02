//go:build darwin
// +build darwin

package proxy

import (
	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// UniversalPortHandler handles all ports on the virtual interface.
// On macOS, this uses pfctl for traffic redirection instead of iptables.
type UniversalPortHandler struct {
	virtualIP     string
	mainProxyIP   string
	mainProxyPort int
	cache         cache.Cache
	config        *config.Config
	running       bool
}

// NewUniversalPortHandler creates a new universal port handler for macOS.
func NewUniversalPortHandler(
	virtualIP string,
	mainProxyIP string,
	mainProxyPort int,
	cache cache.Cache,
	cfg *config.Config,
) *UniversalPortHandler {
	return &UniversalPortHandler{
		virtualIP:     virtualIP,
		mainProxyIP:   mainProxyIP,
		mainProxyPort: mainProxyPort,
		cache:         cache,
		config:        cfg,
		running:       false,
	}
}

// Start sets up traffic redirection on macOS.
// Note: Universal port handling on macOS requires pfctl rules which need sudo.
// For now, we log a warning and gracefully skip this feature.
func (uph *UniversalPortHandler) Start() error {
	if uph.running {
		return nil
	}

	logger.Log.Info("⚠️  Universal port handler on macOS requires pfctl configuration")
	logger.Log.Info("ℹ️  Skipping universal port redirection - use specific ports instead")
	logger.Log.Debugf("Note: To enable universal port handling on macOS, pfctl rules would be needed")
	logger.Log.Debugf("Redirect rule would be: %s:* -> %s:%d", uph.virtualIP, uph.mainProxyIP, uph.mainProxyPort)

	uph.running = true
	return nil
}

// Stop removes traffic redirection rules.
func (uph *UniversalPortHandler) Stop() error {
	if !uph.running {
		return nil
	}

	logger.Log.Debug("Stopping universal port handler (macOS - no cleanup needed)")
	uph.running = false
	return nil
}

// IsRunning returns whether the handler is running.
func (uph *UniversalPortHandler) IsRunning() bool {
	return uph.running
}

// EnhancedPortManager combines the original port manager with universal port handling.
// On macOS, universal port handling is limited.
type EnhancedPortManager struct {
	*PortManager
	universalHandler *UniversalPortHandler
	cache            cache.Cache
	config           *config.Config
}

// NewEnhancedPortManager creates a new enhanced port manager for macOS.
func NewEnhancedPortManager(
	virtualIP string,
	mainProxyIP string,
	mainProxyPort int,
	cache cache.Cache,
	cfg *config.Config,
	proxyHandler *Proxy,
) *EnhancedPortManager {
	originalPM := NewPortManager(mainProxyIP, mainProxyPort, cfg, proxyHandler)
	universalHandler := NewUniversalPortHandler(virtualIP, mainProxyIP, mainProxyPort, cache, cfg)

	return &EnhancedPortManager{
		PortManager:      originalPM,
		universalHandler: universalHandler,
		cache:            cache,
		config:           cfg,
	}
}

// StartUniversalPortHandling starts the universal port handling for the virtual interface.
func (epm *EnhancedPortManager) StartUniversalPortHandling() error {
	return epm.universalHandler.Start()
}

// StopUniversalPortHandling stops the universal port handling.
func (epm *EnhancedPortManager) StopUniversalPortHandling() error {
	return epm.universalHandler.Stop()
}

// StartWithUniversalHandling starts both the original port manager and universal handling.
func (epm *EnhancedPortManager) StartWithUniversalHandling() error {
	// Start universal port handling (on macOS this just logs a warning)
	if err := epm.StartUniversalPortHandling(); err != nil {
		logger.Log.Warnf("Universal port handling not available: %v", err)
		// Don't fail - continue without universal port handling
	}

	logger.Log.Info("Enhanced port manager started (macOS mode)")
	return nil
}

// UpdateMainProxyPort updates the main proxy port for the universal handler.
func (epm *EnhancedPortManager) UpdateMainProxyPort(port int) {
	epm.universalHandler.mainProxyPort = port
}

// StopAll stops all port handling.
func (epm *EnhancedPortManager) StopAll() error {
	// Stop universal handling (no-op on macOS)
	if err := epm.StopUniversalPortHandling(); err != nil {
		logger.Log.Warnf("Error stopping universal handling: %v", err)
	}

	logger.Log.Info("Enhanced port manager stopped")
	return nil
}
