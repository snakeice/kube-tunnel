package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	protocolHTTP = "http"
	protocolGRPC = "grpc"
)

func New(c cache.Cache, hm *health.Monitor, cfg *config.Config) *Proxy {
	return &Proxy{cache: c, health: hm, cfg: cfg}
}

func (p *Proxy) HandleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Fast path: Try to use cached setup and proxy
	if cachedProxy, _ := p.tryFastPath(w, r, startTime); cachedProxy != nil {
		return
	}

	// Fallback to regular path
	p.handleProxyRegular(w, r, startTime)
}

func (p *Proxy) tryFastPath(
	w http.ResponseWriter,
	r *http.Request,
	startTime time.Time,
) (*CustomReverseProxy, *SetupCacheEntry) {
	hostParts := strings.SplitN(r.Host, ".", 3)
	if len(hostParts) < 2 {
		return nil, nil
	}

	service := hostParts[0]
	namespace := hostParts[1]
	isGRPC := strings.Contains(r.Header.Get("Content-Type"), "grpc") ||
		strings.Contains(r.Header.Get("User-Agent"), "grpc")

	cacheKey := ProxyCacheKey{Service: service, Namespace: namespace, IsGRPC: isGRPC}

	if cachedSetupInterface, ok := p.setupCache.Load(cacheKey); ok {
		cachedProxy, cachedSetup := p.tryUseCachedSetup(
			cachedSetupInterface,
			cacheKey,
			w,
			r,
			service,
			startTime,
		)
		if cachedProxy != nil && cachedSetup != nil {
			return cachedProxy, cachedSetup
		}
	}

	return nil, nil
}

// tryUseCachedSetup attempts to use cached setup if valid.
func (p *Proxy) tryUseCachedSetup(
	cachedSetupInterface any,
	cacheKey ProxyCacheKey,
	w http.ResponseWriter,
	r *http.Request,
	service string,
	startTime time.Time,
) (*CustomReverseProxy, *SetupCacheEntry) {
	cachedSetup, ok := cachedSetupInterface.(*SetupCacheEntry)
	if !ok || time.Now().After(cachedSetup.Expiry) {
		p.setupCache.Delete(cacheKey)
		p.proxyCache.Delete(cacheKey)
		return nil, nil
	}

	cachedProxyInterface, exists := p.proxyCache.Load(cacheKey)
	if !exists {
		return nil, nil
	}

	cachedProxy, ok := cachedProxyInterface.(*CustomReverseProxy)
	if !ok {
		return nil, nil
	}

	responseWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
	r.Header.Set("X-Internal-Request", "1")
	cachedProxy.ServeHTTP(responseWriter, r)

	duration := time.Since(startTime)
	if duration > 100*time.Millisecond {
		logger.LogDebug("Fast path slower than expected", logrus.Fields{
			"service":  service,
			"duration": duration.String(),
		})
	}

	return cachedProxy, cachedSetup
}

func (p *Proxy) handleProxyRegular(w http.ResponseWriter, r *http.Request, startTime time.Time) {
	isGRPC := isGRPCRequest(r)
	protocolInfo := getProtocolInfo(r)

	logger.LogRequest(r.Method, r.URL.Path, protocolInfo, r.RemoteAddr)

	if handleCORS(w, r) {
		return
	}

	service, namespace, err := parseAndValidateHost(w, r, isGRPC)
	if err != nil {
		return
	}

	localIP, localPort, err := p.setupPortForwardUltraFast(service, namespace, r)
	if err != nil {
		p.handlePortForwardError(w, service, namespace, isGRPC, err)
		return
	}

	cacheKey := ProxyCacheKey{Service: service, Namespace: namespace, IsGRPC: isGRPC}
	cachedProxy := p.getOrCreateCachedProxy(cacheKey, localIP, localPort, isGRPC)

	setupEntry := &SetupCacheEntry{
		LocalIP:   localIP,
		LocalPort: localPort,
		Expiry:    time.Now().Add(5 * time.Minute),
	}
	p.setupCache.Store(cacheKey, setupEntry)

	responseWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
	cachedProxy.ServeHTTP(responseWriter, r)

	totalDuration := time.Since(startTime)
	p.recordMetricsFast(responseWriter, r, service, namespace, isGRPC, totalDuration)
}

// ProxyRequest contains the parameters for proxy execution.
type ProxyRequest struct {
	ResponseWriter *responseWriterWrapper
	Request        *http.Request
	Service        string
	Namespace      string
	LocalIP        string
	LocalPort      int
	IsGRPC         bool
	StartTime      time.Time
	RequestSize    int64
}

// handlePortForwardError handles port forward errors.
func (p *Proxy) handlePortForwardError(
	w http.ResponseWriter, service, namespace string, isGRPC bool, err error,
) {
	logger.LogError(fmt.Sprintf("Port-forward failed for %s.%s", service, namespace), err)
	if isGRPC {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "14")
		w.Header().Set("Grpc-Message", "Service Unavailable")
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "Error creating port-forward", http.StatusInternalServerError)
	}
}

// HandleHealthCheck mirrors HealthCheckHandler.
func (p *Proxy) HandleHealthCheck(
	w http.ResponseWriter,
	r *http.Request,
) {
	p.healthCheckHandler(w, r)
}

// HandleHealthStatus mirrors HealthStatusHandler.
func (p *Proxy) HandleHealthStatus(w http.ResponseWriter, r *http.Request) {
	p.healthStatusHandler(w, r)
}

// HandleHealthMetrics mirrors HealthMetricsHandler.
func (p *Proxy) HandleHealthMetrics(w http.ResponseWriter, r *http.Request) {
	p.healthMetricsHandler(w, r)
}

// HandlePrometheusMetrics serves Prometheus metrics.
func (p *Proxy) HandlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	promhttp.Handler().ServeHTTP(w, r)
}

func (p *Proxy) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)
	resp := map[string]any{
		"status":   "healthy",
		"protocol": protocolInfo,
		"version":  "1.0.0",
		"features": []string{"http/1.1", "h2c", "h2", "grpc"},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.LogError("Failed to encode health check", err)
	}
}

func (p *Proxy) healthStatusHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var all map[string]*health.Status
	if p.health != nil {
		all = p.health.GetAllHealthStatus()
	} else {
		all = map[string]*health.Status{}
	}
	list := make([]map[string]any, 0, len(all))
	for k, s := range all {
		list = append(
			list,
			map[string]any{
				"service":       k,
				"healthy":       s.IsHealthy,
				"last_checked":  s.LastChecked.Format(time.RFC3339),
				"last_healthy":  s.LastHealthy.Format(time.RFC3339),
				"failure_count": s.FailureCount,
				"response_time": s.ResponseTime.Milliseconds(),
				"error_message": s.ErrorMessage,
			},
		)
	}
	resp := map[string]any{
		"status":          "ok",
		"protocol":        protocolInfo,
		"monitor_enabled": p.cfg.Health.Enabled,
		"check_interval":  p.cfg.Health.CheckInterval.String(),
		"total_services":  len(all),
		"services":        list,
		"timestamp":       time.Now().Format(time.RFC3339),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// setupPortForwardUltraFast sets up port forwarding with minimal overhead.
func (p *Proxy) setupPortForwardUltraFast(
	service, namespace string,
	r *http.Request,
) (string, int, error) {
	originalPort := extractOriginalPort(r)

	// Direct cache call without context timeout for maximum speed
	var localIP string
	var localPort int
	var err error

	if originalPort > 0 {
		localIP, localPort, err = p.cache.EnsurePortForwardWithHint(
			service,
			namespace,
			originalPort,
		)
	} else {
		localIP, localPort, err = p.cache.EnsurePortForward(service, namespace)
	}

	return localIP, localPort, err
}

// getOrCreateCachedProxy gets or creates a cached proxy instance.
func (p *Proxy) getOrCreateCachedProxy(
	cacheKey ProxyCacheKey,
	localIP string,
	localPort int,
	isGRPC bool,
) *CustomReverseProxy {
	if cachedInterface, exists := p.proxyCache.Load(cacheKey); exists {
		if cachedProxy, ok := cachedInterface.(*CustomReverseProxy); ok {
			return cachedProxy
		}
		p.proxyCache.Delete(cacheKey)
	}

	// Create new proxy with optimized settings
	proxy := createReverseProxy(localIP, localPort, isGRPC)
	p.proxyCache.Store(cacheKey, proxy)
	return proxy
}

// recordMetricsFast records metrics with minimal overhead.
func (p *Proxy) recordMetricsFast(
	responseWriter *responseWriterWrapper,
	r *http.Request,
	service, namespace string,
	isGRPC bool,
	duration time.Duration,
) {
	// Only record essential metrics for performance
	protocol := protocolHTTP
	if isGRPC {
		protocol = protocolGRPC
	}

	// Log minimal metrics
	logger.LogDebug("Request completed", logrus.Fields{
		"service":   service,
		"namespace": namespace,
		"method":    r.Method,
		"status":    responseWriter.statusCode,
		"duration":  duration.String(),
		"protocol":  protocol,
	})
}

func (p *Proxy) healthMetricsHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var all map[string]*health.Status
	if p.health != nil {
		all = p.health.GetAllHealthStatus()
	} else {
		all = map[string]*health.Status{}
	}
	total := len(all)
	healthyCount := 0

	for _, s := range all {
		if s.IsHealthy {
			healthyCount++
		}
	}

	resp := map[string]any{
		"status":             "ok",
		"protocol":           protocolInfo,
		"monitor_enabled":    p.cfg.Health.Enabled,
		"total_services":     total,
		"healthy_services":   healthyCount,
		"unhealthy_services": total - healthyCount,
		"health_ratio":       fmt.Sprintf("%.2f", float64(healthyCount)/float64(max(total, 1))),
		"service_performance": map[string]any{
			"total_services":  total,
			"healthy_count":   healthyCount,
			"unhealthy_count": total - healthyCount,
			"note":            "Individual service performance details available at /health/status",
			"status_endpoint": "/health/status",
		},
		"configuration": map[string]any{
			"check_interval": p.cfg.Health.CheckInterval.String(),
			"timeout":        p.cfg.Health.Timeout.String(),
			"max_failures":   p.cfg.Health.MaxFailures,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Handler preserves existing API while migrating callers to struct methods.
// It deliberately avoids a package-level singleton to satisfy gochecknoglobals;
// lightweight objects are created on demand. If construction becomes expensive
// in the future we can introduce an internal sync.Once guarded instance.
// Deprecated: package-level Handler removed in favor of DI; kept only if
// external callers still rely on it. It now constructs minimal dependencies.
// Consider removing entirely once migration complete.
// func Handler(w http.ResponseWriter, r *http.Request) {}
