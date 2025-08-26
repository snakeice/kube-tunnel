package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/metrics"
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

	// Debug logging for request details
	logger.LogDebug("🔍 Processing request", logrus.Fields{
		"method":               r.Method,
		"url":                  r.URL.String(),
		"host":                 r.Host,
		"headers":              fmt.Sprintf("%v", r.Header),
		"remote_addr":          r.RemoteAddr,
		"user_agent":           r.Header.Get("User-Agent"),
		"has_context_deadline": func() bool { _, ok := r.Context().Deadline(); return ok }(),
	})

	// Create a timeout context, but ensure it doesn't override a longer client timeout
	var ctx context.Context
	var cancel context.CancelFunc

	if deadline, ok := r.Context().Deadline(); ok {
		// Client already has a deadline, respect it if it's longer than our timeout
		proxyDeadline := time.Now().Add(p.cfg.Performance.ProxyTimeout)
		logger.LogDebug("🕒 Client has deadline", logrus.Fields{
			"client_deadline":          deadline,
			"proxy_deadline":           proxyDeadline,
			"will_use_client_deadline": deadline.After(proxyDeadline),
		})
		if deadline.After(proxyDeadline) {
			// Client deadline is longer, use our timeout
			ctx, cancel = context.WithTimeout(r.Context(), p.cfg.Performance.ProxyTimeout)
		} else {
			// Client deadline is shorter or similar, use client's context
			ctx, cancel = context.WithCancel(r.Context())
		}
	} else {
		// No client deadline, use our timeout
		ctx, cancel = context.WithTimeout(r.Context(), p.cfg.Performance.ProxyTimeout)
	}
	defer cancel()
	r = r.WithContext(ctx)

	requestSize := max(r.ContentLength, 0)
	logger.LogRequest(r.Method, r.URL.Path, protocolInfo, r.RemoteAddr)

	if handleCORS(w, r) {
		return
	}

	service, namespace, err := parseAndValidateHost(w, r, isGRPC)
	if err != nil {
		return
	}

	localIP, localPort, err := p.setupPortForward(service, namespace, r)
	if err != nil {
		p.handlePortForwardError(w, service, namespace, isGRPC, err)
		return
	}

	p.checkBackendHealth(service, namespace, localIP, localPort)

	responseWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
	p.executeProxy(
		responseWriter, r, service, namespace,
		localIP, localPort, isGRPC, startTime, requestSize,
	)
}

// setupPortForward handles the port forwarding setup logic.
func (p *Proxy) setupPortForward(service, namespace string, r *http.Request) (string, int, error) {
	originalPort := extractOriginalPort(r)

	var localIP string
	var localPort int
	var err error

	// Try to use the same port for Kubernetes port-forwarding if available
	if originalPort > 0 {
		localIP, localPort, err = p.cache.EnsurePortForwardWithHint(
			service,
			namespace,
			originalPort,
		)
	} else {
		localIP, localPort, err = p.cache.EnsurePortForward(service, namespace)
	}

	if err != nil {
		return "", 0, err
	}

	return localIP, localPort, nil
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

// executeProxy executes the actual proxy request and records metrics.
func (p *Proxy) executeProxy(
	responseWriter *responseWriterWrapper,
	r *http.Request,
	service, namespace, localIP string,
	localPort int,
	isGRPC bool,
	startTime time.Time,
	requestSize int64,
) {
	rp := createReverseProxy(localIP, localPort, isGRPC, responseWriter)

	rp.ServeHTTP(responseWriter, r)
	totalDuration := time.Since(startTime)

	p.recordMetrics(
		responseWriter,
		r,
		service,
		namespace,
		isGRPC,
		totalDuration,
		requestSize,
	)
}

// recordMetrics records health and performance metrics.
func (p *Proxy) recordMetrics(
	responseWriter *responseWriterWrapper,
	r *http.Request,
	service, namespace string,
	isGRPC bool,
	totalDuration time.Duration,
	requestSize int64,
) {
	// Store real request response time in health monitor for dashboard display
	if p.health != nil && p.cache != nil {
		serviceKey := fmt.Sprintf("%s.%s", service, namespace)
		p.health.UpdateServicePerformance(
			serviceKey,
			totalDuration,
			responseWriter.statusCode < 400,
		)
	}

	// Record real request metrics
	if p.cache != nil {
		metrics.RecordRequestMetrics(
			service,
			namespace,
			r.Method,
			responseWriter.statusCode,
			totalDuration,
			requestSize,
			responseWriter.responseSize,
		)
	}

	logger.LogResponseMetrics(
		r.Method,
		r.URL.Path,
		responseWriter.statusCode,
		totalDuration,
		responseWriter.responseSize,
		isGRPC,
	)
}

// 3. Standard ports based on scheme.
func extractOriginalPort(r *http.Request) int {
	// Check X-Forwarded-Port header first (set by our port manager)
	if forwardedPort := r.Header.Get("X-Forwarded-Port"); forwardedPort != "" {
		if port, err := strconv.Atoi(forwardedPort); err == nil && port > 0 && port <= 65535 {
			return port
		}
	}

	// Check the Host header for port information
	host := r.Host
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		if len(parts) == 2 {
			if port, err := strconv.Atoi(parts[1]); err == nil && port > 0 && port <= 65535 {
				return port
			}
		}
	}

	// If no explicit port, try to infer from scheme or use common defaults
	// This is useful when traffic comes through iptables redirects
	if r.TLS != nil {
		return 443 // HTTPS default
	}
	return 80 // HTTP default
}

// checkBackendHealth - only log actual health issues.
func (p *Proxy) checkBackendHealth(service, namespace, localIP string, localPort int) {
	if !p.cfg.Health.Enabled {
		return
	}

	// Perform basic TCP health check
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", localIP, localPort), 3*time.Second)
	if err != nil {
		logger.Log.WithFields(logrus.Fields{
			"service":   service,
			"namespace": namespace,
			"port":      localPort,
			"error":     err.Error(),
		}).Warn("🏥 Backend health check failed")
		return
	}

	err = conn.Close()
	if err != nil {
		logger.Log.WithFields(logrus.Fields{
			"service":   service,
			"namespace": namespace,
			"port":      localPort,
			"error":     err.Error(),
		}).Warn("🏥 Backend health check failed")
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
