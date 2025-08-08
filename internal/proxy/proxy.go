package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

// responseWriterWrapper wraps http.ResponseWriter to capture metrics.
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode   int
	responseSize int64
	written      bool
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	if !w.written {
		w.statusCode = statusCode
		w.written = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterWrapper) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.responseSize += int64(n)
	return n, err
}

// isGRPCRequest checks if the request is a gRPC request.
func isGRPCRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	// Check for gRPC content types and also check for gRPC-specific headers
	return strings.HasPrefix(contentType, "application/grpc") ||
		r.Header.Get("Grpc-Encoding") != "" ||
		r.Header.Get("Grpc-Accept-Encoding") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "grpc")
}

// getProtocolInfo returns detailed protocol information.
func getProtocolInfo(r *http.Request) string {
	protocol := r.Proto
	if r.TLS != nil {
		protocol += " (TLS)"
		if r.TLS.NegotiatedProtocol != "" {
			protocol += " ALPN:" + r.TLS.NegotiatedProtocol
		}
	} else {
		protocol += " (cleartext)"
	}

	if isGRPCRequest(r) {
		protocol += " gRPC"
	}

	return protocol
}

// createTransport creates an optimized transport for the target protocol.
func createTransport(
	cfg config.PerformanceConfig,
	isHTTPS bool,
	protocol string,
) http.RoundTripper {
	if protocol == "h2c" {
		return &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
			// Use configurable HTTP/2 settings
			MaxHeaderListSize:  262144, // 256KB
			DisableCompression: false,
			ReadIdleTimeout:    30 * time.Second,
			PingTimeout:        15 * time.Second,
		}
	}

	transport := &http.Transport{
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleTimeout,
		TLSHandshakeTimeout:   5 * time.Second,        // Keep optimized
		ExpectContinueTimeout: 500 * time.Millisecond, // Keep optimized
		DisableCompression:    false,
		ForceAttemptHTTP2:     cfg.ForceHTTP2,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     false,
	}

	if isHTTPS {
		transport.TLSClientConfig = loadTLSConfig()
	}

	return transport
}

// getRetryConfig returns retry configuration from environment variables.
func getRetryConfig() (int, time.Duration) {
	maxRetries := 2
	baseDelay := 100 * time.Millisecond

	if envRetries := os.Getenv("PROXY_MAX_RETRIES"); envRetries != "" {
		if parsed, err := strconv.Atoi(envRetries); err == nil && parsed >= 0 && parsed <= 10 {
			maxRetries = parsed
		}
	}

	if envDelay := os.Getenv("PROXY_RETRY_DELAY_MS"); envDelay != "" {
		if parsed, err := strconv.Atoi(envDelay); err == nil && parsed >= 25 && parsed <= 2000 {
			baseDelay = time.Duration(parsed) * time.Millisecond
		}
	}

	return maxRetries, baseDelay
}

// retryableTransport wraps a transport with retry logic for connection failures.
type retryableTransport struct {
	base       http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
	isGRPC     bool
}

func (rt *retryableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	requestStartTime := time.Now()

	// Log initial attempt
	logger.LogRetryAttempt(0, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)

	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		attemptStartTime := time.Now()
		// Check if context is canceled before each attempt
		select {
		case <-req.Context().Done():
			logger.LogConnectionCanceled(req.Method, req.URL.Path, attempt)
			return nil, req.Context().Err()
		default:
		}

		// Clone the request for each retry attempt
		reqClone := req.Clone(req.Context())

		// Log each attempt
		if attempt > 0 {
			logger.LogRetryAttempt(attempt, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)
		}

		resp, err := rt.base.RoundTrip(reqClone)
		attemptDuration := time.Since(attemptStartTime)

		if err == nil {
			if attempt > 0 {
				logger.LogRetrySuccessWithTiming(
					attempt,
					attemptDuration,
					time.Since(requestStartTime),
				)
			}
			return resp, nil
		}

		// Check if this is a retryable error
		if !isRetryableError(err) {
			logger.LogNonRetryableError(req.Method, req.URL.Path, err, rt.isGRPC)
			return nil, err
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < rt.maxRetries {
			delay := min(
				// Faster exponential backoff
				rt.baseDelay*time.Duration(1<<attempt),
				// Cap at 1 second instead of 5
				1*time.Second)

			logger.LogRetryWithTiming(attempt+1, delay.String(), err, attemptDuration)

			// Use context-aware sleep to allow cancellation
			select {
			case <-req.Context().Done():
				logger.LogConnectionCanceled(req.Method, req.URL.Path, attempt+1)
				return nil, req.Context().Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	totalDuration := time.Since(requestStartTime)
	logger.LogRetryFailedWithTiming(rt.maxRetries+1, lastErr, totalDuration)
	return nil, lastErr
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Retry on connection refused, timeout, and network unreachable errors
	// Also include gRPC-specific connection errors and protocol mismatches
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "dial tcp") ||
		strings.Contains(errStr, "connect: connection refused") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection timed out") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "malformed HTTP response") ||
		strings.Contains(errStr, "http2: client connection lost") ||
		strings.Contains(errStr, "stream error") ||
		strings.Contains(errStr, "HTTP/1.x transport connection broken")
}

// healthCheckBackend performs a quick health check on the backend service.
func healthCheckBackend(port int, timeout time.Duration) error {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	// Try a simple HEAD request to check if the service is responding
	resp, err := client.Head(fmt.Sprintf("http://localhost:%d/", port))
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.LogError("Failed to close response body", err)
		}
	}()

	// Accept any HTTP response (even 404) as long as something is responding
	return nil
}

// protocolFallbackTransport tries different protocols on failure.
type protocolFallbackTransport struct {
	port       int
	isGRPC     bool
	maxRetries int
	baseDelay  time.Duration

	config config.PerformanceConfig
}

func (pft *protocolFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Skip protocol fallback if disabled
	if pft.config.DisableProtocolFallback {
		protocol := "h2c"
		if !pft.isGRPC {
			protocol = "http/1.1"
		}
		transport := createTransport(pft.config, false, protocol)
		retryTransport := &retryableTransport{
			base:       transport,
			maxRetries: pft.maxRetries,
			baseDelay:  pft.baseDelay,
			isGRPC:     pft.isGRPC,
		}
		return retryTransport.RoundTrip(req)
	}

	protocols := []string{"h2c", "http/1.1"}
	if !pft.isGRPC {
		protocols = []string{"http/1.1", "h2c"}
	}

	var lastErr error

	for _, protocol := range protocols {
		logger.LogDebug("Trying protocol", logrus.Fields{
			"protocol": protocol,
			"grpc":     pft.isGRPC,
			"attempt":  protocol,
		})

		transport := createTransport(pft.config, false, protocol)
		retryTransport := &retryableTransport{
			base:       transport,
			maxRetries: pft.maxRetries,
			baseDelay:  pft.baseDelay,
			isGRPC:     pft.isGRPC,
		}

		resp, err := retryTransport.RoundTrip(req)
		if err == nil {
			logger.LogDebug("Protocol successful", logrus.Fields{
				"protocol": protocol,
				"grpc":     pft.isGRPC,
			})
			return resp, nil
		}

		// If it's a protocol mismatch error, try the next protocol
		if strings.Contains(err.Error(), "malformed HTTP response") ||
			strings.Contains(err.Error(), "HTTP/1.x transport connection broken") {
			logger.LogDebug("Protocol mismatch, trying next", logrus.Fields{
				"protocol": protocol,
				"error":    err.Error(),
			})
			lastErr = err
			continue
		}

		// For other errors, return immediately
		return nil, err
	}

	return nil, lastErr
}

// createProtocolFallbackTransport creates a transport that tries multiple protocols.
func createProtocolFallbackTransport(port int, isGRPC bool) http.RoundTripper {
	maxRetries, baseDelay := getRetryConfig()

	return &protocolFallbackTransport{
		port:       port,
		isGRPC:     isGRPC,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
	}
}

// handleCORS sets CORS headers and handles preflight requests.
// It returns true if the request was a preflight request and has been handled.
func handleCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().
		Set("Access-Control-Allow-Headers", "Content-Type, Authorization, grpc-timeout, grpc-encoding, grpc-accept-encoding")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return true
	}
	return false
}

// parseAndValidateHost parses the service and namespace from the request host.
// It writes an error response if parsing fails.
func parseAndValidateHost(
	w http.ResponseWriter,
	r *http.Request,
	isGRPC bool,
) (string, string, error) {
	service, namespace, err := tools.ParseHost(r.Host)
	if err != nil {
		logger.LogError(fmt.Sprintf("Failed to parse host '%s'", r.Host), err)
		if isGRPC {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "3") // INVALID_ARGUMENT
			w.Header().Set("Grpc-Message", "Invalid host format")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Invalid host format", http.StatusBadRequest)
		}
		return "", "", err
	}
	return service, namespace, nil
}

// setupPortForward ensures a port-forward is established for the service.
// It writes an error response if the port-forward fails.
func setupPortForward(
	w http.ResponseWriter,
	service, namespace string,
	isGRPC bool,
) (int, error) {
	// Use the cache from the default proxy instance so that legacy handler
	// path still works after introducing the Proxy struct.
	localPort, err := getDefault().cache.EnsurePortForward(service, namespace)
	if err != nil {
		logger.LogError(fmt.Sprintf("Port-forward failed for %s.%s", service, namespace), err)
		if isGRPC {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "14") // UNAVAILABLE
			w.Header().Set("Grpc-Message", "Service Unavailable")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Error creating port-forward", http.StatusInternalServerError)
		}
		return 0, err
	}
	logger.LogDebug(
		"Port-forward ready",
		logrus.Fields{"service": service, "namespace": namespace, "port": localPort},
	)
	return localPort, nil
}

// checkBackendHealth logs the health status of the backend and performs a quick check if unhealthy.
func checkBackendHealth(service, namespace string, localPort int) {
	serviceKey := fmt.Sprintf("%s.%s", service, namespace)
	healthMonitor := health.GetHealthMonitor()
	if healthMonitor == nil {
		return // No health monitor configured
	}

	healthStatus := healthMonitor.IsHealthy(serviceKey)
	if healthStatus == nil {
		return // No status available for this service yet
	}

	// Simplify nested ifs: early return on healthy status
	if healthStatus.IsHealthy {
		logger.LogDebug("Backend healthy according to monitor", logrus.Fields{
			"service":     service,
			"namespace":   namespace,
			"port":        localPort,
			"response_ms": healthStatus.ResponseTime.Milliseconds(),
		})
		return
	}

	// Unhealthy path
	logger.LogDebug("Backend reported unhealthy by monitor", logrus.Fields{
		"service":       service,
		"namespace":     namespace,
		"port":          localPort,
		"failure_count": healthStatus.FailureCount,
		"last_error":    healthStatus.ErrorMessage,
		"last_healthy":  healthStatus.LastHealthy,
	})

	// Perform quick health check if not skipped
	cfg := config.GetConfig()
	if cfg.Performance.SkipHealthCheck {
		return
	}
	if err := healthCheckBackend(localPort, 500*time.Millisecond); err != nil {
		logger.LogBackendHealth(localPort, "unhealthy")
		logger.LogDebug("Quick health validation failed",
			logrus.Fields{
				"port":  localPort,
				"error": err.Error(),
			})
	} else {
		logger.LogBackendHealth(localPort, "recovered")
	}
}

// setGRPCHeaders sets the necessary headers for a gRPC request.
func setGRPCHeaders(req *http.Request) {
	if req.Header.Get("TE") == "" {
		req.Header.Set("TE", "trailers")
	}
	cfg := config.GetConfig()
	req.Header.Set("Grpc-Timeout", cfg.Performance.GRPCTimeout)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "kube-tunnel-grpc/1.0")
	}
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/grpc") {
		req.Header.Set("Content-Type", "application/grpc+proto")
	}
}

// proxyDirector creates the director function for the reverse proxy.
func proxyDirector(localPort int, isGRPC bool) func(req *http.Request) {
	return func(req *http.Request) {
		originalProto := req.Proto
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("localhost:%d", localPort)

		if req.Header.Get("Connection") == "" && req.ProtoMajor == 2 {
			req.Header.Del("Connection")
		}
		if deadline, ok := req.Context().Deadline(); ok {
			req.Header.Set("X-Request-Deadline", deadline.Format(time.RFC3339))
		}

		if isGRPC {
			setGRPCHeaders(req)
		}
		logger.LogProxy(req.Method, req.URL.Path, originalProto, "auto-detect", isGRPC)
	}
}

// proxyModifyResponse creates the modify response function for the reverse proxy.
func proxyModifyResponse(w *responseWriterWrapper, isGRPC bool) func(*http.Response) error {
	return func(resp *http.Response) error {
		w.statusCode = resp.StatusCode
		if resp.ContentLength > 0 {
			w.responseSize = resp.ContentLength
		}

		if isGRPC {
			if resp.Header.Get("Content-Type") == "" {
				resp.Header.Set("Content-Type", "application/grpc+proto")
			}
			if resp.Header.Get("Grpc-Status") == "" && resp.StatusCode == http.StatusOK {
				resp.Header.Set("Grpc-Status", "0") // OK
			}
		}
		return nil
	}
}

// proxyErrorHandler creates the error handler function for the reverse proxy.
func proxyErrorHandler(isGRPC bool) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logger.LogDebug(
				"Request canceled by client",
				logrus.Fields{"method": r.Method, "path": r.URL.Path, "error": err.Error()},
			)
			return
		}

		// Downgrade noisy benign errors
		errStr := err.Error()
		if strings.Contains(errStr, "read on closed response body") ||
			strings.Contains(errStr, "connection reset by peer") {
			logger.LogDebug("Upstream closed connection", logrus.Fields{
				"method": r.Method, "path": r.URL.Path, "error": errStr,
			})
			return
		}

		if isRetryableError(err) {
			logger.LogError("Connection failed after retries", err)
		} else {
			logger.LogProxyError(r.Method, r.URL.Path, err)
		}

		if isGRPC {
			grpcStatus := "14" // UNAVAILABLE
			if strings.Contains(err.Error(), "timeout") {
				grpcStatus = "4" // DEADLINE_EXCEEDED
			}
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", grpcStatus)
			w.Header().Set("Grpc-Message", fmt.Sprintf("Proxy Error: %v", err))
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, fmt.Sprintf("Bad Gateway: %v", err), http.StatusBadGateway)
		}
	}
}

// createReverseProxy configures and returns a new httputil.ReverseProxy.
func createReverseProxy(
	localPort int,
	isGRPC bool,
	w *responseWriterWrapper,
) *httputil.ReverseProxy {
	transport := createProtocolFallbackTransport(localPort, isGRPC)
	rp := &httputil.ReverseProxy{
		Director:  proxyDirector(localPort, isGRPC),
		Transport: transport,
		ModifyResponse: proxyModifyResponse( //nolint:bodyclose // Handled in the response writer
			w,
			isGRPC,
		),
		ErrorHandler:  proxyErrorHandler(isGRPC),
		FlushInterval: 50 * time.Millisecond,
	}
	// Suppress default noisy log output; route through custom adapter
	rp.ErrorLog = log.New(&reverseProxyLogAdapter{}, "", 0)
	return rp
}

// reverseProxyLogAdapter intercepts standard library ReverseProxy logs and downgrades
// expected transient network errors to debug level while preserving other messages.
type reverseProxyLogAdapter struct{}

func (a *reverseProxyLogAdapter) Write(p []byte) (int, error) { //nolint:revive // adapter signature
	msg := strings.TrimSpace(string(p))
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "reverseproxy read error") && strings.Contains(lower, "read on closed response body") {
		logger.LogDebug("Suppressed benign reverse proxy read error", logrus.Fields{"msg": msg})
		return len(p), nil
	}
	if strings.Contains(lower, "reverseproxy read error") && strings.Contains(lower, "connection reset by peer") {
		logger.LogDebug("Upstream connection reset", logrus.Fields{"msg": msg})
		return len(p), nil
	}
	logger.LogDebug("ReverseProxy log", logrus.Fields{"msg": msg})
	return len(p), nil
}

// legacyHandler retains the original implementation. The exported Handler symbol
// now delegates to the struct-based handler in handler.go keeping backwards compatibility.
func legacyHandler(w http.ResponseWriter, r *http.Request) {
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
		return // Error response already sent
	}

	localPort, err := setupPortForward(w, service, namespace, isGRPC)
	if err != nil {
		return // Error response already sent
	}

	checkBackendHealth(service, namespace, localPort)

	responseWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
	proxy := createReverseProxy(localPort, isGRPC, responseWriter)

	logger.LogDebug("Proxying with protocol fallback", logrus.Fields{
		"service": service, "namespace": namespace, "is_grpc": isGRPC, "local_port": localPort,
	})

	proxyStartTime := time.Now()
	proxy.ServeHTTP(responseWriter, r)
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

// HealthCheckHandler provides a simple health check endpoint.
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)

	response := map[string]any{
		"status":   "healthy",
		"protocol": protocolInfo,
		"version":  "1.0.0",
		"features": []string{"http/1.1", "h2c", "h2", "grpc"},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Simple JSON response
	fmt.Fprintf(w, `{
		"status": "%s",
		"protocol": "%s",
		"version": "%s",
		"features": ["http/1.1", "h2c", "h2", "grpc"]
	}`, response["status"], protocolInfo, response["version"])

	logger.LogHealthCheck(protocolInfo)
}

// HealthStatusHandler provides health status for all monitored services.
func HealthStatusHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	healthMonitor := health.GetHealthMonitor()
	var allStatus map[string]*health.Status
	if healthMonitor != nil {
		allStatus = healthMonitor.GetAllHealthStatus()
	} else {
		allStatus = make(map[string]*health.Status)
	}

	// Convert to API-friendly format
	statusList := make([]map[string]any, 0, len(allStatus))
	for serviceKey, status := range allStatus {
		statusList = append(statusList, map[string]any{
			"service":       serviceKey,
			"healthy":       status.IsHealthy,
			"last_checked":  status.LastChecked.Format(time.RFC3339),
			"last_healthy":  status.LastHealthy.Format(time.RFC3339),
			"failure_count": status.FailureCount,
			"response_time": status.ResponseTime.Milliseconds(),
			"error_message": status.ErrorMessage,
		})
	}

	cfg := config.GetConfig()
	response := map[string]any{
		"status":          "ok",
		"protocol":        protocolInfo,
		"monitor_enabled": cfg.Health.Enabled,
		"check_interval":  cfg.Health.CheckInterval.String(),
		"total_services":  len(allStatus),
		"services":        statusList,
		"timestamp":       time.Now().Format(time.RFC3339),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Log.WithError(err).Error("Failed to encode health status response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Log.WithFields(logrus.Fields{
		"protocol":      protocolInfo,
		"service_count": len(allStatus),
		"client_ip":     r.RemoteAddr,
	}).Info("📊 Health status requested")
}

// HealthMetricsHandler provides aggregate health metrics.
func HealthMetricsHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	healthMonitor := health.GetHealthMonitor()
	var allStatus map[string]*health.Status
	if healthMonitor != nil {
		allStatus = healthMonitor.GetAllHealthStatus()
	} else {
		allStatus = make(map[string]*health.Status)
	}

	// Calculate metrics
	totalServices := len(allStatus)
	healthyServices := 0
	unhealthyServices := 0
	var totalResponseTime time.Duration
	var maxResponseTime time.Duration
	var minResponseTime = time.Hour // Initialize to high value

	for _, status := range allStatus {
		if status.IsHealthy {
			healthyServices++
		} else {
			unhealthyServices++
		}

		totalResponseTime += status.ResponseTime
		if status.ResponseTime > maxResponseTime {
			maxResponseTime = status.ResponseTime
		}
		if status.ResponseTime < minResponseTime && status.ResponseTime > 0 {
			minResponseTime = status.ResponseTime
		}
	}

	var avgResponseTime time.Duration
	if totalServices > 0 {
		avgResponseTime = totalResponseTime / time.Duration(totalServices)
	}
	if minResponseTime == time.Hour {
		minResponseTime = 0
	}

	healthRatio := float64(healthyServices) / float64(max(totalServices, 1))

	cfg := config.GetConfig()
	response := map[string]any{
		"status":             "ok",
		"protocol":           protocolInfo,
		"monitor_enabled":    cfg.Health.Enabled,
		"total_services":     totalServices,
		"healthy_services":   healthyServices,
		"unhealthy_services": unhealthyServices,
		"health_ratio":       fmt.Sprintf("%.2f", healthRatio),
		"response_times": map[string]any{
			"average_ms": avgResponseTime.Milliseconds(),
			"min_ms":     minResponseTime.Milliseconds(),
			"max_ms":     maxResponseTime.Milliseconds(),
		},
		"configuration": map[string]any{
			"check_interval": cfg.Health.CheckInterval.String(),
			"timeout":        cfg.Health.Timeout.String(),
			"max_failures":   cfg.Health.MaxFailures,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Log.WithError(err).Error("Failed to encode health statistics response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Log.WithFields(logrus.Fields{
		"protocol":         protocolInfo,
		"total_services":   totalServices,
		"healthy_services": healthyServices,
		"health_ratio":     healthRatio,
		"client_ip":        r.RemoteAddr,
	}).Info("📈 Health metrics requested")
}

// loadTLSConfig loads TLS configuration with proper cipher suites for HTTP/2.
func loadTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
		NextProtos: []string{"h2", "http/1.1"},
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
		},
		PreferServerCipherSuites: true,
	}
}
