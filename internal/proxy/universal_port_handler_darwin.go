//go:build darwin
// +build darwin

package proxy

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// UniversalPortHandler handles all ports on the virtual interface.
// On macOS, this uses multiple port listeners since pfctl requires complex setup.
type UniversalPortHandler struct {
	virtualIP     string
	mainProxyIP   string
	mainProxyPort int
	cache         cache.Cache
	config        *config.Config
	running       bool
	mu            sync.RWMutex
	listeners     map[int]net.Listener
	servers       map[int]*http.Server
	proxyHandler  http.Handler
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
		listeners:     make(map[int]net.Listener),
		servers:       make(map[int]*http.Server),
	}
}

// SetProxyHandler sets the proxy handler for all port listeners.
func (uph *UniversalPortHandler) SetProxyHandler(handler http.Handler) {
	uph.mu.Lock()
	defer uph.mu.Unlock()
	uph.proxyHandler = handler
}

// Start sets up traffic redirection on macOS by listening on common ports.
func (uph *UniversalPortHandler) Start() error {
	uph.mu.Lock()
	defer uph.mu.Unlock()

	if uph.running {
		return nil
	}

	// Common Kubernetes service ports to listen on
	commonPorts := []int{8080, 8081, 9090, 3000, 5000, 8000, 8443, 9000, 8888, 3001}

	successCount := 0
	for _, port := range commonPorts {
		if port == uph.mainProxyPort {
			continue // Skip the main proxy port
		}

		if err := uph.startPortListener(port); err != nil {
			logger.Log.Debugf("Could not start listener on port %d: %v", port, err)
			// Continue with other ports
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		logger.Log.Infof("✅ Universal port handler started on %d additional ports", successCount)
	} else {
		logger.Log.Info("ℹ️  No additional port listeners started (ports may be in use)")
	}

	uph.running = true
	return nil
}

// startPortListener starts a listener on a specific port.
func (uph *UniversalPortHandler) startPortListener(port int) error {
	// Try to bind to the virtual IP first, then localhost
	bindAddrs := []string{uph.virtualIP, "127.0.0.1", "0.0.0.0"}

	var listener net.Listener
	var err error
	var boundAddr string

	for _, addr := range bindAddrs {
		bindStr := net.JoinHostPort(addr, strconv.Itoa(port))
		listener, err = net.Listen("tcp", bindStr)
		if err == nil {
			boundAddr = addr
			break
		}
	}

	if err != nil {
		return fmt.Errorf("failed to bind port %d on any address: %w", port, err)
	}

	// Create HTTP server that wraps the proxy handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add the original port header for the main proxy to use
		r.Header.Set("X-Original-Port", strconv.Itoa(port))
		r.Header.Set("X-Forwarded-Port", strconv.Itoa(port))

		if uph.proxyHandler != nil {
			uph.proxyHandler.ServeHTTP(w, r)
		} else {
			http.Error(w, "Proxy not initialized", http.StatusServiceUnavailable)
		}
	})

	server := &http.Server{
		Handler: handler,
	}

	uph.listeners[port] = listener
	uph.servers[port] = server

	// Start serving in goroutine
	go func() {
		logger.Log.Debugf("🔌 Listening on %s:%d for Kubernetes services", boundAddr, port)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Log.Debugf("Server on port %d stopped: %v", port, err)
		}
	}()

	return nil
}

// Stop removes traffic redirection rules and stops all listeners.
func (uph *UniversalPortHandler) Stop() error {
	uph.mu.Lock()
	defer uph.mu.Unlock()

	if !uph.running {
		return nil
	}

	// Stop all servers
	for port, server := range uph.servers {
		if err := server.Close(); err != nil {
			logger.Log.Debugf("Error closing server on port %d: %v", port, err)
		}
	}

	// Close all listeners
	for port, listener := range uph.listeners {
		if err := listener.Close(); err != nil {
			logger.Log.Debugf("Error closing listener on port %d: %v", port, err)
		}
	}

	uph.listeners = make(map[int]net.Listener)
	uph.servers = make(map[int]*http.Server)
	uph.running = false

	logger.Log.Debug("Universal port handler stopped")
	return nil
}

// IsRunning returns whether the handler is running.
func (uph *UniversalPortHandler) IsRunning() bool {
	uph.mu.RLock()
	defer uph.mu.RUnlock()
	return uph.running
}

// GetListeningPorts returns the list of ports being listened on.
func (uph *UniversalPortHandler) GetListeningPorts() []int {
	uph.mu.RLock()
	defer uph.mu.RUnlock()

	ports := make([]int, 0, len(uph.listeners))
	for port := range uph.listeners {
		ports = append(ports, port)
	}
	return ports
}

// EnhancedPortManager combines the original port manager with universal port handling.
// On macOS, universal port handling is limited.
type EnhancedPortManager struct {
	*PortManager
	universalHandler *UniversalPortHandler
	cache            cache.Cache
	config           *config.Config
	proxyHandler     *Proxy
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
		proxyHandler:     proxyHandler,
	}
}

// StartUniversalPortHandling starts the universal port handling for the virtual interface.
func (epm *EnhancedPortManager) StartUniversalPortHandling() error {
	// Set the proxy handler before starting
	if epm.proxyHandler != nil {
		epm.universalHandler.SetProxyHandler(http.HandlerFunc(epm.proxyHandler.HandleProxy))
	}
	return epm.universalHandler.Start()
}

// StopUniversalPortHandling stops the universal port handling.
func (epm *EnhancedPortManager) StopUniversalPortHandling() error {
	return epm.universalHandler.Stop()
}

// StartWithUniversalHandling starts both the original port manager and universal handling.
func (epm *EnhancedPortManager) StartWithUniversalHandling() error {
	// Start universal port handling with the proxy handler
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
