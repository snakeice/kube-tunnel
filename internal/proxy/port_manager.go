package proxy

import (
	"context"
	"net"
	"net/http"
	"sync"

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
