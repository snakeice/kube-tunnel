package app

import (
	"context"
	"net/http"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/dns"
	"github.com/snakeice/kube-tunnel/internal/health"
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
	c.Cache = cache.NewCache(c.Monitor)

	c.DNS = dns.NewProxyDNS()
	if err := c.DNS.Start(); err != nil {
		return nil, err
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
	if c.Monitor != nil {
		c.Monitor.Stop()
	}
	if c.DNS != nil {
		if err := c.DNS.Stop(); err != nil {
			return err
		}
	}
	return nil
}
