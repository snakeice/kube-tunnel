package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
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
func New(c cache.Cache, hm *health.Monitor, cfg *config.Config) *Proxy {
	return &Proxy{cache: c, health: hm, cfg: cfg}
}

// HandleProxy is the struct-based equivalent of the previous package-level Handler.
func (p *Proxy) HandleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	isGRPC := isGRPCRequest(r)
	protocolInfo := getProtocolInfo(r)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	requestSize := max(r.ContentLength, 0)
	logger.LogRequest(r.Method, r.URL.Path, protocolInfo, r.RemoteAddr)
	logger.LogRequestStart(r.Method, r.URL.Path, isGRPC, requestSize)

	if handleCORS(w, r) {
		return
	}

	service, namespace, err := parseAndValidateHost(w, r, isGRPC)
	if err != nil {
		return
	}

	localIP, localPort, err := p.cache.EnsurePortForward(service, namespace)
	if err != nil {
		logger.LogError(fmt.Sprintf("Port-forward failed for %s.%s", service, namespace), err)
		if isGRPC {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "14")
			w.Header().Set("Grpc-Message", "Service Unavailable")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Error creating port-forward", http.StatusInternalServerError)
		}
		return
	}
	logger.LogDebug(
		"Port-forward ready",
		logrus.Fields{"service": service, "namespace": namespace, "ip": localIP, "port": localPort},
	)

	p.checkBackendHealth(service, namespace, localIP, localPort)

	responseWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
	rp := createReverseProxy(localIP, localPort, isGRPC, responseWriter)
	logger.LogDebug(
		"Proxying with protocol fallback",
		logrus.Fields{
			"service":    service,
			"namespace":  namespace,
			"is_grpc":    isGRPC,
			"local_ip":   localIP,
			"local_port": localPort,
		},
	)

	proxyStartTime := time.Now()
	rp.ServeHTTP(responseWriter, r)
	proxyDuration := time.Since(proxyStartTime)
	totalDuration := time.Since(startTime)

	logger.LogProxyMetrics(
		service,
		namespace,
		localPort,
		proxyDuration,
		responseWriter.statusCode < 400,
	)
	logger.LogResponseMetrics(
		r.Method,
		r.URL.Path,
		responseWriter.statusCode,
		totalDuration,
		responseWriter.responseSize,
		isGRPC,
	)
}

func (p *Proxy) checkBackendHealth(service, namespace string, localIP string, localPort int) {
	if p.health == nil {
		return
	}
	serviceKey := fmt.Sprintf("%s.%s", service, namespace)
	status := p.health.IsHealthy(serviceKey)
	if status == nil {
		return
	}
	if status.IsHealthy {
		return
	}
	if p.cfg != nil && p.cfg.Performance.SkipHealthCheck {
		return
	}
	if err := healthCheckBackendOnIP(localIP, localPort, 500*time.Millisecond); err != nil {
		logger.LogBackendHealth(localPort, "unhealthy")
	} else {
		logger.LogBackendHealth(localPort, "healthy")
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
	logger.LogHealthCheck(protocolInfo)
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
	var totalRT, maxRT time.Duration
	minRT := time.Hour
	for _, s := range all {
		if s.IsHealthy {
			healthyCount++
		}
		totalRT += s.ResponseTime
		if s.ResponseTime > maxRT {
			maxRT = s.ResponseTime
		}
		if s.ResponseTime > 0 && s.ResponseTime < minRT {
			minRT = s.ResponseTime
		}
	}
	if minRT == time.Hour {
		minRT = 0
	}
	var avgRT time.Duration
	if total > 0 {
		avgRT = totalRT / time.Duration(total)
	}
	resp := map[string]any{
		"status":             "ok",
		"protocol":           protocolInfo,
		"monitor_enabled":    p.cfg.Health.Enabled,
		"total_services":     total,
		"healthy_services":   healthyCount,
		"unhealthy_services": total - healthyCount,
		"health_ratio":       fmt.Sprintf("%.2f", float64(healthyCount)/float64(max(total, 1))),
		"response_times": map[string]any{
			"average_ms": avgRT.Milliseconds(),
			"min_ms":     minRT.Milliseconds(),
			"max_ms":     maxRT.Milliseconds(),
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
