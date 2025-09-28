package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// PortManager manages multiple port listeners that redirect traffic to the main proxy.
type PortManager struct {
	mainProxyPort       int
	mainProxyIP         string
	listeners           map[int]*PortListener
	mu                  sync.RWMutex
	config              *config.Config
	proxyHandler        *Proxy
	virtualInterfaceMgr *UnifiedVirtualInterfaceManager
	useVirtualIF        bool
}

// PortListener represents a listener on a specific port that redirects to the main proxy.
type PortListener struct {
	Port      int
	Server    *http.Server
	Cancel    context.CancelFunc
	Listener  net.Listener
	VirtualIP string // IP of virtual interface if used
}

// NewPortManager creates a new port manager.
func NewPortManager(
	mainProxyIP string,
	mainProxyPort int,
	cfg *config.Config,
	proxyHandler *Proxy,
) *PortManager {
	pm := &PortManager{
		mainProxyPort: mainProxyPort,
		mainProxyIP:   mainProxyIP,
		listeners:     make(map[int]*PortListener),
		config:        cfg,
		proxyHandler:  proxyHandler,
	}
	pm.virtualInterfaceMgr = NewUnifiedVirtualInterfaceManager()

	return pm
}

// EnableVirtualInterface enables virtual interface support on macOS and Linux.
func (pm *PortManager) EnableVirtualInterface() error {
	if pm.virtualInterfaceMgr == nil {
		return fmt.Errorf("virtual interface manager not initialized on %s", runtime.GOOS)
	}

	if err := pm.virtualInterfaceMgr.Enable(); err != nil {
		return fmt.Errorf("virtual interface requirements not met: %w", err)
	}

	pm.useVirtualIF = true
	logger.Log.Info("🖥️  Virtual interface support enabled for " + runtime.GOOS)
	return nil
}

// StartListeningOnCommonPorts starts listeners on commonly used HTTP/HTTPS ports.
func (pm *PortManager) StartListeningOnCommonPorts() error {
	commonPorts := []int{8080, 8443, 3000, 3001, 5000, 8000, 8001, 8888, 9000, 9090}

	for _, port := range commonPorts {
		if port == pm.mainProxyPort {
			continue // Skip the main proxy port
		}

		if err := pm.StartListeningOnPort(port); err != nil {
			logger.LogError(fmt.Sprintf("Failed to start listener on port %d", port), err)
			// Continue with other ports even if one fails
		}
	}

	return nil
}

// StartListeningOnPort starts a listener on a specific port that redirects to the main proxy.
func (pm *PortManager) StartListeningOnPort(port int) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already listening on this port
	if _, exists := pm.listeners[port]; exists {
		return fmt.Errorf("already listening on port %d", port)
	}

	virtualIP, bindIP := pm.setupVirtualInterfaceForPort(port)

	if bindIP == "" {
		bindIP = "0.0.0.0"
	}

	addr := net.JoinHostPort(bindIP, strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create a reverse proxy that forwards to the main proxy
	reverseProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Keep the original host and path
			originalHost := req.Host
			req.URL.Scheme = "http"
			req.URL.Host = net.JoinHostPort(pm.mainProxyIP, strconv.Itoa(pm.mainProxyPort))

			// Preserve the original host in a header for the main proxy to use
			req.Header.Set("X-Forwarded-Host", originalHost)
			req.Header.Set("X-Forwarded-Port", strconv.Itoa(port))
			req.Header.Set("X-Forwarded-Proto", "http")
			if virtualIP != "" {
				req.Header.Set("X-Virtual-Interface-Ip", virtualIP)
			}

			logger.LogDebug("Port redirect", logrus.Fields{
				"from_port":  port,
				"to_port":    pm.mainProxyPort,
				"host":       originalHost,
				"path":       req.URL.Path,
				"virtual_ip": virtualIP,
				"bind_ip":    bindIP,
			})
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.LogError(fmt.Sprintf("Proxy redirect error from port %d", port), err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		},
	}

	server := &http.Server{
		Handler:      reverseProxy,
		ReadTimeout:  pm.config.Performance.ReadTimeout,
		WriteTimeout: pm.config.Performance.WriteTimeout,
		IdleTimeout:  pm.config.Performance.IdleTimeout,
	}

	portListener := &PortListener{
		Port:      port,
		Server:    server,
		Cancel:    cancel,
		Listener:  listener,
		VirtualIP: virtualIP,
	}

	pm.listeners[port] = portListener

	// Start the server in a goroutine
	go func() {
		defer cancel()

		logMsg := fmt.Sprintf("🔀 Port redirector listening on %s -> :%d", addr, pm.mainProxyPort)
		if virtualIP != "" {
			logMsg += fmt.Sprintf(" (virtual IP: %s)", virtualIP)
		}
		logger.Log.Info(logMsg)

		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.LogError(fmt.Sprintf("Port redirector server failed on port %d", port), err)
		}
	}()

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.LogError(fmt.Sprintf("Failed to shutdown port redirector on port %d", port), err)
		}
	}()

	return nil
}

// StartListeningOnPortRange starts listeners on a range of ports.
func (pm *PortManager) StartListeningOnPortRange(startPort, endPort int) error {
	var errors []error

	for port := startPort; port <= endPort; port++ {
		if port == pm.mainProxyPort {
			continue // Skip the main proxy port
		}

		if err := pm.StartListeningOnPort(port); err != nil {
			errors = append(errors, fmt.Errorf("port %d: %w", port, err))
		}
	}

	if len(errors) > 0 {
		logger.Log.Warnf("Failed to start listeners on %d ports", len(errors))
		for _, err := range errors {
			logger.LogError("Port listener error", err)
		}
	}

	return nil
}

// StopListeningOnPort stops listening on a specific port.
func (pm *PortManager) StopListeningOnPort(port int) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	portListener, exists := pm.listeners[port]
	if !exists {
		return fmt.Errorf("not listening on port %d", port)
	}

	// Clean up virtual interface if used
	if portListener.VirtualIP != "" && pm.virtualInterfaceMgr != nil {
		// Remove redirection rule
		if err := pm.virtualInterfaceMgr.RemoveRedirectionRule(
			port, pm.mainProxyPort, portListener.VirtualIP, pm.mainProxyIP,
		); err != nil {
			logger.LogError("Failed to remove redirection rule", err)
		}

		// Remove route
		routeDest := fmt.Sprintf("%s/32", portListener.VirtualIP)
		if err := pm.virtualInterfaceMgr.RemoveRoute(routeDest, "127.0.0.1", fmt.Sprintf("kube-tun-%d", port)); err != nil {
			logger.LogError("Failed to remove route", err)
		}

		// Destroy virtual interface
		interfaceName := fmt.Sprintf("kube-tun-%d", port)
		if err := pm.virtualInterfaceMgr.DestroyVirtualInterface(interfaceName); err != nil {
			logger.LogError("Failed to destroy virtual interface", err)
		}
	}

	portListener.Cancel()
	delete(pm.listeners, port)

	logger.Log.Infof("Stopped listening on port %d", port)
	return nil
}

// StopAll stops all port listeners.
func (pm *PortManager) StopAll() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for port, portListener := range pm.listeners {
		portListener.Cancel()
		logger.Log.Infof("Stopped listening on port %d", port)
	}

	// Clean up all virtual interface resources
	if pm.useVirtualIF && pm.virtualInterfaceMgr != nil {
		if err := pm.virtualInterfaceMgr.Disable(); err != nil {
			logger.LogError("Failed to disable virtual interface management", err)
		}
	}

	pm.listeners = make(map[int]*PortListener)
	return nil
}

// setupVirtualInterfaceForPort handles the creation of a virtual interface for a given port.
// It returns the virtualIP (if created) and the bindIP to be used by the listener.
func (pm *PortManager) setupVirtualInterfaceForPort(port int) (string, string) {
	// Use virtual interface if enabled and supported
	if !pm.useVirtualIF || pm.virtualInterfaceMgr == nil || !pm.virtualInterfaceMgr.IsSupported() {
		return "", pm.config.Network.ProxyBindIP
	}

	// Generate a virtual IP for this port (e.g., 127.0.0.2, 127.0.0.3, etc.)
	virtualIP := fmt.Sprintf("127.0.0.%d", 2+port%250) // Simple IP generation
	interfaceName := fmt.Sprintf("kube-tun-%d", port)

	// Create virtual interface
	if err := pm.virtualInterfaceMgr.CreateVirtualInterface(interfaceName, virtualIP); err != nil {
		logger.LogError(
			fmt.Sprintf(
				"Failed to create virtual interface for port %d, falling back to regular binding",
				port,
			),
			err,
		)
		return "", pm.config.Network.ProxyBindIP
	}

	// If we successfully created the interface, we bind to its IP
	bindIP := virtualIP

	// Add route for the virtual interface
	routeDest := fmt.Sprintf("%s/32", virtualIP)
	if err := pm.virtualInterfaceMgr.AddRoute(routeDest, "127.0.0.1", interfaceName, 100); err != nil {
		logger.LogError(
			fmt.Sprintf("Failed to add route for virtual interface %s", interfaceName),
			err,
		)
	}

	// Add traffic redirection rule
	if err := pm.virtualInterfaceMgr.AddRedirectionRule(port, pm.mainProxyPort, virtualIP, pm.mainProxyIP); err != nil {
		logger.LogError(fmt.Sprintf("Failed to add redirection rule for port %d", port), err)
	}

	return virtualIP, bindIP
}

// GetListeningPorts returns a list of ports currently being listened on.
func (pm *PortManager) GetListeningPorts() []int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	ports := make([]int, 0, len(pm.listeners))
	for port := range pm.listeners {
		ports = append(ports, port)
	}

	return ports
}

// IsListeningOnPort checks if we're listening on a specific port.
func (pm *PortManager) IsListeningOnPort(port int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	_, exists := pm.listeners[port]
	return exists
}

// GetVirtualInterfaceStatistics returns statistics about virtual interfaces.
func (pm *PortManager) GetVirtualInterfaceStatistics() map[string]any {
	stats := map[string]any{
		"virtual_interface_enabled": pm.useVirtualIF,
		"platform":                  runtime.GOOS,
		"supported":                 false,
	}

	if pm.virtualInterfaceMgr != nil {
		stats["supported"] = pm.virtualInterfaceMgr.IsSupported()
		stats["status"] = pm.virtualInterfaceMgr.GetStatus()
	}

	return stats
}

// ValidateVirtualInterfaces checks if all virtual interfaces are properly configured.
func (pm *PortManager) ValidateVirtualInterfaces() error {
	if !pm.useVirtualIF || pm.virtualInterfaceMgr == nil {
		return nil // No virtual interfaces to validate
	}

	// Check if requirements are met
	if err := pm.virtualInterfaceMgr.CheckRequirements(); err != nil {
		return fmt.Errorf("virtual interface requirements not met: %w", err)
	}

	return nil
}
