package app

import (
	"context"
	"net/http"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/dashboard"
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
	Cfg         *config.Config
	Cache       cache.Cache
	Monitor     *health.Monitor
	DNS         *dns.ProxyDNS
	Mux         *http.ServeMux
	Proxy       *proxy.Proxy
	Dashboard   *dashboard.Dashboard
	PortManager *proxy.EnhancedPortManager
}

// Build initializes all components with explicit dependency ordering.
func Build() (*Container, error) {
	cfg := config.GetConfig()
	c := &Container{Cfg: cfg}

	c.Monitor = health.NewHealthMonitor(cfg.Health)
	c.Monitor.Start()

	// Step 1: Create cache with localhost initially (will be updated after virtual interface is created)
	c.Cache = cache.NewCacheWithIP(c.Monitor, cfg, "")

	// For DNS resolution, we need to use the virtual interface IP (where universal port handler listens)
	// not the port-forward IP (where the proxy runs)
	dnsResolveIP := cfg.Network.VirtualInterfaceIP
	if dnsResolveIP == "" {
		// Fallback to port-forward IP if no virtual interface
		dnsResolveIP = c.Cache.GetPortForwardIP()
	}
	logger.Log.Infof("DNS will resolve to IP: %s", dnsResolveIP)

	// Step 2: Start DNS server
	c.DNS = dns.NewProxyDNS(cfg, dnsResolveIP)
	if err := c.DNS.Start(); err != nil {
		return nil, err
	}

	// Step 3: Update cache with port-forward IP if virtual interfaces were created
	c.setupVirtualInterfacesIfEnabled(cfg)

	c.Proxy = proxy.New(c.Cache, c.Monitor, cfg)

	// Initialize enhanced port manager
	mainProxyIP := c.Cache.GetPortForwardIP()
	mainProxyPort := 80 // Default HTTP port - this will be updated by the main function

	if cfg.Network.UseVirtualInterface {
		// Get the virtual interface IP for enhanced port management
		virtualIP := c.DNS.GetVirtualInterfaceIP()
		if virtualIP != "" {
			c.PortManager = proxy.NewEnhancedPortManager(
				virtualIP,
				mainProxyIP,
				mainProxyPort,
				c.Cache,
				cfg,
				c.Proxy,
			)
			logger.Log.Infof(
				"Enhanced port manager initialized for virtual interface: %s",
				virtualIP,
			)
		}
	}

	// Initialize dashboard
	dashboard, err := dashboard.NewDashboard()
	if err != nil {
		return nil, err
	}
	c.Dashboard = dashboard

	c.Mux = http.NewServeMux()
	c.Mux.HandleFunc("/", c.Proxy.HandleProxy)

	// Health endpoints
	c.Mux.HandleFunc("/health", c.Proxy.HandleHealthCheck)
	c.Mux.HandleFunc("/health/status", c.Proxy.HandleHealthStatus)
	c.Mux.HandleFunc("/health/metrics", c.Proxy.HandleHealthMetrics)
	c.Mux.HandleFunc("/metrics", c.Proxy.HandlePrometheusMetrics)

	// Dashboard endpoints
	c.Mux.HandleFunc("/dashboard", c.Dashboard.ServeDashboard)
	c.Mux.HandleFunc("/dashboard/assets/", c.Dashboard.ServeDashboardAssets)

	return c, nil
}

// StartPortManager starts the enhanced port manager if available.
func (c *Container) StartPortManager(mainProxyPort int, virtualInterface bool) error {
	if c.PortManager == nil {
		logger.Log.Debug("No enhanced port manager to start")
		return nil
	}

	// Update the main proxy port
	c.PortManager.UpdateMainProxyPort(mainProxyPort)

	// Enable virtual interface support if requested and available
	if virtualInterface {
		if err := c.PortManager.EnableVirtualInterface(); err != nil {
			logger.LogError("Failed to enable virtual interface support", err)
		}
	}

	// Start the enhanced port manager
	logger.Log.Infof("Starting enhanced port manager with main proxy port: %d", mainProxyPort)

	if err := c.PortManager.StartWithUniversalHandling(); err != nil {
		return err
	}

	logger.Log.Info("Enhanced port manager started successfully")
	return nil
}

// StopPortManager stops the enhanced port manager if running.
func (c *Container) StopPortManager() error {
	if c.PortManager == nil {
		return nil
	}

	logger.Log.Debug("Stopping enhanced port manager...")
	return c.PortManager.StopAll()
}

// Shutdown gracefully stops long running components.
func (c *Container) Shutdown(ctx context.Context) error {
	logger.Log.Info("Shutting down application components...")

	// Stop enhanced port manager
	if err := c.StopPortManager(); err != nil {
		logger.Log.Errorf("Failed to stop port manager: %v", err)
		// Continue with other shutdowns
	}

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

// setupVirtualInterfacesIfEnabled sets up virtual interfaces if enabled in the config
// and updates the cache with the port-forward IP.
func (c *Container) setupVirtualInterfacesIfEnabled(cfg *config.Config) {
	if !cfg.Network.UseVirtualInterface {
		return
	}

	portForwardIP := c.DNS.GetPortForwardIP()
	if portForwardIP == "" || portForwardIP == "127.0.0.1" {
		return
	}

	logger.Log.Infof("Port-forward IP configured: %s", portForwardIP)

	// Get DNS interface IP for rebinding
	dnsIP := c.DNS.GetVirtualInterfaceIP()
	if dnsIP == "" {
		return
	}

	logger.Log.Infof("DNS virtual interface created with IP: %s", dnsIP)

	// Rebind DNS server to virtual interface IP
	if err := c.DNS.RebindToVirtualInterface(dnsIP); err != nil {
		logger.Log.Warnf("Failed to rebind DNS server to virtual interface: %v", err)
		logger.Log.Infof("DNS server will continue running on localhost")
	} else {
		logger.Log.Infof("DNS server successfully bound to virtual interface IP: %s", dnsIP)
	}

	// Update the cache with the port-forward IP (may be different from DNS IP)
	c.Cache = cache.NewCacheWithIP(c.Monitor, cfg, portForwardIP)
	logger.Log.Infof("Updated cache with port forward IP: %s", portForwardIP)
}
