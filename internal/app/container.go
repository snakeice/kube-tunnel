package app

import (
	"context"
	"net/http"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/dns"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/proxy"
)

// Container wires core application services together. Acts as a very small
// dependency injection root without pulling an external framework.
// Elements are exposed via interfaces to enable testing and future refactors.
//
// Clean architecture layering decision:
// - config: infrastructure boundary (provides runtime config)
// - cache / dns / health: infrastructure services
// - proxy: delivery (HTTP) depending only on abstractions from inner layers
//
// The container owns startup/shutdown orchestration.
type Container struct {
	Cfg     *config.Config
	Cache   cache.Cache
	Monitor *health.Monitor
	DNS     *dns.ProxyDNS
	Mux     *http.ServeMux
	Proxy   *proxy.Proxy
}

// Build initializes all components with explicit dependency ordering.
func Build() (*Container, error) {
	cfg := config.GetConfig()
	c := &Container{Cfg: cfg}

	c.Monitor = health.NewHealthMonitor(cfg.Health)
	c.Monitor.Start()

	// Step 1: Create cache with localhost initially (will be updated after virtual interface is created)
	c.Cache = cache.NewCacheWithIP(c.Monitor, cfg, "")
	portForwardIP := c.Cache.GetPortForwardIP()
	logger.Log.Infof("Initial port forward IP configured: %s", portForwardIP)

	// Step 2: Start DNS server on localhost first
	c.DNS = dns.NewProxyDNS(cfg, portForwardIP)
	if err := c.DNS.Start(); err != nil {
		return nil, err
	}

	// Step 3: Update cache with virtual interface IP if it was created
	if cfg.Network.UseVirtualInterface {
		// Try to get the actual virtual interface IP from the DNS server
		if actualIP := c.DNS.GetVirtualInterfaceIP(); actualIP != "" {
			logger.Log.Infof("Virtual interface created with IP: %s", actualIP)

			// Rebind DNS server to virtual interface IP
			if err := c.DNS.RebindToVirtualInterface(actualIP); err != nil {
				logger.Log.Warnf("Failed to rebind DNS server to virtual interface: %v", err)
				logger.Log.Infof("DNS server will continue running on localhost")
			} else {
				logger.Log.Infof("DNS server successfully bound to virtual interface IP: %s", actualIP)
			}

			// Update the cache with the actual IP
			c.Cache = cache.NewCacheWithIP(c.Monitor, cfg, actualIP)
			logger.Log.Infof("Updated port forward IP to: %s", actualIP)
		}
	}

	c.Proxy = proxy.New(c.Cache, c.Monitor, cfg)
	c.Mux = http.NewServeMux()
	c.Mux.HandleFunc("/", c.Proxy.HandleProxy)
	c.Mux.HandleFunc("/health", c.Proxy.HandleHealthCheck)
	c.Mux.HandleFunc("/health/status", c.Proxy.HandleHealthStatus)
	c.Mux.HandleFunc("/health/metrics", c.Proxy.HandleHealthMetrics)

	return c, nil
}

// Shutdown gracefully stops long running components.
func (c *Container) Shutdown(ctx context.Context) error {
	logger.Log.Info("Shutting down application components...")

	// Stop health monitor
	if c.Monitor != nil {
		logger.Log.Debug("Stopping health monitor...")
		c.Monitor.Stop()
	}

	// Stop DNS server and clean up virtual interface
	if c.DNS != nil {
		logger.Log.Debug("Stopping DNS server...")
		if err := c.DNS.Stop(); err != nil {
			logger.Log.Errorf("Failed to stop DNS server: %v", err)
			return err
		}
	}

	logger.Log.Info("Application shutdown completed successfully")
	return nil
}
