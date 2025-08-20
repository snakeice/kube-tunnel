package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// PortManager manages multiple port listeners that redirect traffic to the main proxy.
type PortManager struct {
	mainProxyPort int
	mainProxyIP   string
	listeners     map[int]*PortListener
	mu            sync.RWMutex
	config        *config.Config
	proxyHandler  *Proxy
}

// PortListener represents a listener on a specific port that redirects to the main proxy.
type PortListener struct {
	Port     int
	Server   *http.Server
	Cancel   context.CancelFunc
	Listener net.Listener
}

// NewPortManager creates a new port manager.
func NewPortManager(
	mainProxyIP string,
	mainProxyPort int,
	cfg *config.Config,
	proxyHandler *Proxy,
) *PortManager {
	return &PortManager{
		mainProxyPort: mainProxyPort,
		mainProxyIP:   mainProxyIP,
		listeners:     make(map[int]*PortListener),
		config:        cfg,
		proxyHandler:  proxyHandler,
	}
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

	// Use virtual interface IP if available, otherwise bind to all interfaces
	bindIP := "0.0.0.0"
	if pm.config.Network.UseVirtualInterface && pm.config.Network.VirtualInterfaceIP != "" {
		bindIP = pm.config.Network.VirtualInterfaceIP
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

			logger.LogDebug("Port redirect", logrus.Fields{
				"from_port": port,
				"to_port":   pm.mainProxyPort,
				"host":      originalHost,
				"path":      req.URL.Path,
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
		Port:     port,
		Server:   server,
		Cancel:   cancel,
		Listener: listener,
	}

	pm.listeners[port] = portListener

	// Start the server in a goroutine
	go func() {
		defer cancel()
		logger.Log.Infof("🔀 Port redirector listening on %s -> :%d", addr, pm.mainProxyPort)

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

	pm.listeners = make(map[int]*PortListener)
	return nil
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
