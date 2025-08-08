package proxy

import (
	"net/http"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
)

// Proxy encapsulates dependencies required to handle proxy and health endpoints.
// This supports a cleaner architecture by separating construction from usage
// and allowing dependency injection (useful for tests / future interfaces).
type Proxy struct {
	cache  cache.Cache
	health *health.Monitor
	cfg    *config.Config
}

// New creates a new Proxy handler container.
func New(c cache.Cache, hm *health.Monitor) *Proxy {
	cfg := config.GetConfig()
	return &Proxy{cache: c, health: hm, cfg: &cfg}
}

// HandleProxy is the struct-based equivalent of the previous package-level Handler.
func (p *Proxy) HandleProxy(w http.ResponseWriter, r *http.Request) { legacyHandler(w, r) }

// HandleHealthCheck mirrors HealthCheckHandler.
func (p *Proxy) HandleHealthCheck(w http.ResponseWriter, r *http.Request) { HealthCheckHandler(w, r) }

// HandleHealthStatus mirrors HealthStatusHandler.
func (p *Proxy) HandleHealthStatus(w http.ResponseWriter, r *http.Request) { HealthStatusHandler(w, r) }

// HandleHealthMetrics mirrors HealthMetricsHandler.
func (p *Proxy) HandleHealthMetrics(w http.ResponseWriter, r *http.Request) {
	HealthMetricsHandler(w, r)
}

// Backward compatibility: keep package-level handlers delegating to a default instance.
var defaultProxy *Proxy

// getDefault returns a lazily-created default Proxy using the global cache implementation and health monitor.
func getDefault() *Proxy {
	if defaultProxy == nil {
		defaultProxy = New(cache.NewCache(), health.GetHealthMonitor())
	}
	return defaultProxy
}

// Handler preserves existing API while migrating callers to struct methods.
func Handler(w http.ResponseWriter, r *http.Request) { getDefault().HandleProxy(w, r) }
