package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
)

// Performance configuration from environment variables.
type PerformanceConfig struct {
	SkipHealthCheck         bool
	ForceHTTP2              bool
	DisableProtocolFallback bool
	MaxIdleConns            int
	MaxIdleConnsPerHost     int
	MaxConnsPerHost         int
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxConcurrentStreams    uint32
	MaxFrameSize            uint32
	GRPCTimeout             string
}

var perfConfig = loadPerformanceConfig()

func loadPerformanceConfig() PerformanceConfig {
	config := PerformanceConfig{
		// Defaults
		SkipHealthCheck:         false,
		ForceHTTP2:              true,
		DisableProtocolFallback: false,
		MaxIdleConns:            200,
		MaxIdleConnsPerHost:     50,
		MaxConnsPerHost:         100,
		ReadTimeout:             30 * time.Second,
		WriteTimeout:            30 * time.Second,
		IdleTimeout:             120 * time.Second,
		MaxConcurrentStreams:    1000,
		MaxFrameSize:            1048576,
		GRPCTimeout:             "30S",
	}

	if os.Getenv("SKIP_HEALTH_CHECK") == "true" {
		config.SkipHealthCheck = true
	}
	if os.Getenv("FORCE_HTTP2") == "false" {
		config.ForceHTTP2 = false
	}
	if os.Getenv("DISABLE_PROTOCOL_FALLBACK") == "true" {
		config.DisableProtocolFallback = true
	}
	if val := os.Getenv("MAX_IDLE_CONNS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			config.MaxIdleConns = parsed
		}
	}
	if val := os.Getenv("MAX_IDLE_CONNS_PER_HOST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			config.MaxIdleConnsPerHost = parsed
		}
	}
	if val := os.Getenv("MAX_CONNS_PER_HOST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			config.MaxConnsPerHost = parsed
		}
	}
	if val := os.Getenv("READ_TIMEOUT"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.ReadTimeout = parsed
		}
	}
	if val := os.Getenv("WRITE_TIMEOUT"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.WriteTimeout = parsed
		}
	}
	if val := os.Getenv("IDLE_TIMEOUT"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.IdleTimeout = parsed
		}
	}
	if val := os.Getenv("MAX_CONCURRENT_STREAMS"); val != "" {
		if parsed, err := strconv.ParseUint(val, 10, 32); err == nil {
			config.MaxConcurrentStreams = uint32(parsed)
		}
	}
	if val := os.Getenv("MAX_FRAME_SIZE"); val != "" {
		if parsed, err := strconv.ParseUint(val, 10, 32); err == nil {
			config.MaxFrameSize = uint32(parsed)
		}
	}
	if val := os.Getenv("GRPC_TIMEOUT"); val != "" {
		config.GRPCTimeout = val
	}

	return config
}

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
func createTransport(isHTTPS bool, protocol string) http.RoundTripper {
	if protocol == "h2c" {
		return &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
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
		MaxIdleConns:          perfConfig.MaxIdleConns,
		MaxIdleConnsPerHost:   perfConfig.MaxIdleConnsPerHost,
		MaxConnsPerHost:       perfConfig.MaxConnsPerHost,
		IdleConnTimeout:       perfConfig.IdleTimeout,
		TLSHandshakeTimeout:   5 * time.Second,        // Keep optimized
		ExpectContinueTimeout: 500 * time.Millisecond, // Keep optimized
		DisableCompression:    false,
		ForceAttemptHTTP2:     perfConfig.ForceHTTP2,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     false,
	}

	if isHTTPS {
		transport.TLSClientConfig = loadTLSConfig()
	}

	return transport
}

// getRetryConfig returns retry configuration from environment variables.
func getRetryConfig() (maxRetries int, baseDelay time.Duration) {
	maxRetries = 2                     // Reduced from 3 for faster failures
	baseDelay = 100 * time.Millisecond // Reduced from 200ms

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
	LogRetryAttempt(0, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)

	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		attemptStartTime := time.Now()
		// Check if context is canceled before each attempt
		select {
		case <-req.Context().Done():
			LogConnectionCanceled(req.Method, req.URL.Path, attempt)
			return nil, req.Context().Err()
		default:
		}

		// Clone the request for each retry attempt
		reqClone := req.Clone(req.Context())

		// Log each attempt
		if attempt > 0 {
			LogRetryAttempt(attempt, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)
		}

		resp, err := rt.base.RoundTrip(reqClone)
		attemptDuration := time.Since(attemptStartTime)

		if err == nil {
			if attempt > 0 {
				LogRetrySuccessWithTiming(attempt, attemptDuration, time.Since(requestStartTime))
			}
			return resp, nil
		}

		// Check if this is a retryable error
		if !isRetryableError(err) {
			LogNonRetryableError(req.Method, req.URL.Path, err, rt.isGRPC)
			return nil, err
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < rt.maxRetries {
			delay := min(
				// Faster exponential backoff
				rt.baseDelay*time.Duration(1<<uint(attempt)),
				// Cap at 1 second instead of 5
				1*time.Second)

			LogRetryWithTiming(attempt+1, delay.String(), err, attemptDuration)

			// Use context-aware sleep to allow cancellation
			select {
			case <-req.Context().Done():
				LogConnectionCanceled(req.Method, req.URL.Path, attempt+1)
				return nil, req.Context().Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	totalDuration := time.Since(requestStartTime)
	LogRetryFailedWithTiming(rt.maxRetries+1, lastErr, totalDuration)
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
func healthCheckBackend(port int32, timeout time.Duration) error {
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
	defer resp.Body.Close()

	// Accept any HTTP response (even 404) as long as something is responding
	return nil
}

// protocolFallbackTransport tries different protocols on failure.
type protocolFallbackTransport struct {
	port       int32
	isGRPC     bool
	maxRetries int
	baseDelay  time.Duration
}

func (pft *protocolFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Skip protocol fallback if disabled
	if perfConfig.DisableProtocolFallback {
		protocol := "h2c"
		if !pft.isGRPC {
			protocol = "http/1.1"
		}
		transport := createTransport(false, protocol)
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
		LogDebug("Trying protocol", logrus.Fields{
			"protocol": protocol,
			"grpc":     pft.isGRPC,
			"attempt":  protocol,
		})

		transport := createTransport(false, protocol)
		retryTransport := &retryableTransport{
			base:       transport,
			maxRetries: pft.maxRetries,
			baseDelay:  pft.baseDelay,
			isGRPC:     pft.isGRPC,
		}

		resp, err := retryTransport.RoundTrip(req)
		if err == nil {
			LogDebug("Protocol successful", logrus.Fields{
				"protocol": protocol,
				"grpc":     pft.isGRPC,
			})
			return resp, nil
		}

		// If it's a protocol mismatch error, try the next protocol
		if strings.Contains(err.Error(), "malformed HTTP response") ||
			strings.Contains(err.Error(), "HTTP/1.x transport connection broken") {
			LogDebug("Protocol mismatch, trying next", logrus.Fields{
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
func createProtocolFallbackTransport(port int32, isGRPC bool) http.RoundTripper {
	maxRetries, baseDelay := getRetryConfig()

	return &protocolFallbackTransport{
		port:       port,
		isGRPC:     isGRPC,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
	}
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	isGRPC := isGRPCRequest(r)
	protocolInfo := getProtocolInfo(r)

	// Add shorter timeout context for faster failures
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	// Get request size
	requestSize := max(r.ContentLength, 0)

	LogRequest(r.Method, r.URL.Path, protocolInfo, r.RemoteAddr)
	LogRequestStart(r.Method, r.URL.Path, isGRPC, requestSize)

	// Set CORS headers for browser compatibility
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().
		Set("Access-Control-Allow-Headers", "Content-Type, Authorization, grpc-timeout, grpc-encoding, grpc-accept-encoding")

	// Handle preflight requests
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	host := r.Host
	service, namespace, err := parseHost(host)
	if err != nil {
		LogError(fmt.Sprintf("Failed to parse host '%s'", host), err)
		if isGRPC {
			// gRPC error response
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "3") // INVALID_ARGUMENT
			w.Header().Set("Grpc-Message", "Invalid host format")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Invalid host format", http.StatusBadRequest)
		}
		return
	}

	LogRouting(service, namespace)
	localPort, err := ensurePortForward(service, namespace)
	if err != nil {
		LogError(fmt.Sprintf("Port-forward failed for %s.%s", service, namespace), err)
		if isGRPC {
			// gRPC error response
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "14") // UNAVAILABLE
			w.Header().Set("Grpc-Message", "Service Unavailable")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Error creating port-forward", http.StatusInternalServerError)
		}
		return
	}

	LogDebug("Port-forward ready", logrus.Fields{
		"service":   service,
		"namespace": namespace,
		"port":      localPort,
	})

	// Use background health monitor status instead of inline checks
	serviceKey := fmt.Sprintf("%s.%s", service, namespace)
	healthy := true
	var healthStatus *HealthStatus
	if globalHealthMonitor != nil {
		healthy, healthStatus = globalHealthMonitor.IsHealthy(serviceKey)
	} else {
		healthStatus = &HealthStatus{IsHealthy: true, ResponseTime: 0}
	}

	if !healthy {
		LogDebug("Backend reported unhealthy by monitor", logrus.Fields{
			"service":       service,
			"namespace":     namespace,
			"port":          localPort,
			"failure_count": healthStatus.FailureCount,
			"last_error":    healthStatus.ErrorMessage,
			"last_healthy":  healthStatus.LastHealthy,
		})

		// For unhealthy services, do a quick validation before proceeding
		if !perfConfig.SkipHealthCheck {
			if err := healthCheckBackend(localPort, 500*time.Millisecond); err != nil {
				LogBackendHealth(localPort, "unhealthy")
				LogDebug("Quick health validation failed, proceeding anyway", logrus.Fields{
					"port":  localPort,
					"error": err.Error(),
				})
			} else {
				LogBackendHealth(localPort, "recovered")
			}
		}
	} else {
		LogDebug("Backend healthy according to monitor", logrus.Fields{
			"service":     service,
			"namespace":   namespace,
			"port":        localPort,
			"response_ms": healthStatus.ResponseTime.Milliseconds(),
		})
	}

	// Use protocol fallback transport for better compatibility
	transport := createProtocolFallbackTransport(localPort, isGRPC)

	// Create response writer wrapper to capture metrics
	responseWriter := &responseWriterWrapper{
		ResponseWriter: w,
		statusCode:     200,
		responseSize:   0,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			originalProto := req.Proto

			req.URL.Scheme = "http"
			req.URL.Host = fmt.Sprintf("localhost:%d", localPort)

			// Preserve important headers for protocol negotiation
			if req.Header.Get("Connection") == "" && req.ProtoMajor == 2 {
				// HTTP/2 doesn't use Connection header
				req.Header.Del("Connection")
			}

			// Add context deadline for retries
			if deadline, ok := req.Context().Deadline(); ok {
				req.Header.Set("X-Request-Deadline", deadline.Format(time.RFC3339))
			}

			if isGRPCRequest(req) {
				if req.Header.Get("TE") == "" {
					req.Header.Set("TE", "trailers")
				}
				// Add gRPC-specific timeout handling
				if req.Header.Get("Grpc-Timeout") == "" {
					req.Header.Set("Grpc-Timeout", perfConfig.GRPCTimeout)
				}
				// Set User-Agent for gRPC if not present
				if req.Header.Get("User-Agent") == "" {
					req.Header.Set("User-Agent", "kube-tunnel-grpc/1.0")
				}
				// Ensure proper content-type for gRPC
				if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/grpc") {
					req.Header.Set("Content-Type", "application/grpc+proto")
				}
				LogProxy(req.Method, req.URL.Path, originalProto, "auto-detect", true)
			} else {
				LogProxy(req.Method, req.URL.Path, originalProto, "auto-detect", false)
			}
		},
		Transport: transport,
		ModifyResponse: func(resp *http.Response) error {
			// Capture response metrics
			responseWriter.statusCode = resp.StatusCode
			if resp.ContentLength > 0 {
				responseWriter.responseSize = resp.ContentLength
			}

			if isGRPC {
				// Add missing gRPC headers if needed
				if resp.Header.Get("Content-Type") == "" {
					resp.Header.Set("Content-Type", "application/grpc+proto")
				}
				if resp.Header.Get("Grpc-Status") == "" && resp.StatusCode == http.StatusOK {
					resp.Header.Set("Grpc-Status", "0") // OK
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Check if this was a context cancellation
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				LogDebug("Request canceled by client", logrus.Fields{
					"method": r.Method,
					"path":   r.URL.Path,
					"error":  err.Error(),
				})
				// Don't write response for canceled requests
				return
			}

			// Log different message for retryable vs non-retryable errors
			if isRetryableError(err) {
				LogError("Connection failed after retries", err)
			} else {
				LogProxyError(r.Method, r.URL.Path, err)
			}

			if isGRPCRequest(r) {
				// Set appropriate gRPC status based on error type
				grpcStatus := "14" // UNAVAILABLE (default)
				if strings.Contains(err.Error(), "timeout") {
					grpcStatus = "4" // DEADLINE_EXCEEDED
				} else if strings.Contains(err.Error(), "connection refused") {
					grpcStatus = "14" // UNAVAILABLE
				}

				w.Header().Set("Content-Type", "application/grpc")
				w.Header().Set("Grpc-Status", grpcStatus)
				w.Header().Set("Grpc-Message", fmt.Sprintf("Proxy Error: %v", err))
				w.WriteHeader(http.StatusOK)
			} else {
				http.Error(w, fmt.Sprintf("Bad Gateway: %v", err), http.StatusBadGateway)
			}
		},
		FlushInterval: time.Millisecond * 50, // Reduced from 100ms for faster streaming
	}

	// Log retry configuration for this request
	LogDebug("Proxying with protocol fallback", logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"is_grpc":    isGRPC,
		"local_port": localPort,
	})

	// Track overall proxy operation
	proxyStartTime := time.Now()
	proxy.ServeHTTP(responseWriter, r)
	proxyDuration := time.Since(proxyStartTime)
	totalDuration := time.Since(startTime)

	// Log final metrics
	LogProxyMetrics(service, namespace, localPort, proxyDuration, responseWriter.statusCode < 400)
	LogResponseMetrics(
		r.Method,
		r.URL.Path,
		responseWriter.statusCode,
		totalDuration,
		responseWriter.responseSize,
		isGRPC,
	)
}

// healthCheckHandler provides a simple health check endpoint.
func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
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

	LogHealthCheck(protocolInfo)
}

// servicesHandler provides information about discovered services.
func servicesHandler(w http.ResponseWriter, r *http.Request, zeroconfServer *ZeroconfServer) {
	protocolInfo := getProtocolInfo(r)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if zeroconfServer == nil {
		response := map[string]any{
			"status":   "unavailable",
			"message":  "Zeroconf server not running",
			"services": []string{},
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	services := zeroconfServer.GetAllServices()

	// Convert to API-friendly format
	serviceList := make([]map[string]any, 0, len(services))
	for domain, info := range services {
		serviceList = append(serviceList, map[string]any{
			"domain":    domain,
			"service":   info.Service,
			"namespace": info.Namespace,
			"port":      info.Port,
			"last_seen": info.LastSeen.Format(time.RFC3339),
		})
	}

	response := map[string]any{
		"status":       "ok",
		"protocol":     protocolInfo,
		"total_count":  len(services),
		"services":     serviceList,
		"last_updated": time.Now().Format(time.RFC3339),
	}

	_ = json.NewEncoder(w).Encode(response)

	log.WithFields(logrus.Fields{
		"protocol":      protocolInfo,
		"service_count": len(services),
		"client_ip":     r.RemoteAddr,
	}).Info("📋 Services list requested")
}

// healthStatusHandler provides health status for all monitored services
func healthStatusHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	healthMonitor := GetHealthMonitor()
	var allStatus map[string]*HealthStatus
	if healthMonitor != nil {
		allStatus = healthMonitor.GetAllHealthStatus()
	} else {
		allStatus = make(map[string]*HealthStatus)
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

	response := map[string]any{
		"status":          "ok",
		"protocol":        protocolInfo,
		"monitor_enabled": healthConfig.Enabled,
		"check_interval":  healthConfig.CheckInterval.String(),
		"total_services":  len(allStatus),
		"services":        statusList,
		"timestamp":       time.Now().Format(time.RFC3339),
	}

	_ = json.NewEncoder(w).Encode(response)

	log.WithFields(logrus.Fields{
		"protocol":      protocolInfo,
		"service_count": len(allStatus),
		"client_ip":     r.RemoteAddr,
	}).Info("📊 Health status requested")
}

// healthMetricsHandler provides aggregate health metrics
func healthMetricsHandler(w http.ResponseWriter, r *http.Request) {
	protocolInfo := getProtocolInfo(r)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	healthMonitor := GetHealthMonitor()
	var allStatus map[string]*HealthStatus
	if healthMonitor != nil {
		allStatus = healthMonitor.GetAllHealthStatus()
	} else {
		allStatus = make(map[string]*HealthStatus)
	}

	// Calculate metrics
	totalServices := len(allStatus)
	healthyServices := 0
	unhealthyServices := 0
	var totalResponseTime time.Duration
	var maxResponseTime time.Duration
	var minResponseTime time.Duration = time.Hour // Initialize to high value

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

	response := map[string]any{
		"status":             "ok",
		"protocol":           protocolInfo,
		"monitor_enabled":    healthConfig.Enabled,
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
			"check_interval": healthConfig.CheckInterval.String(),
			"timeout":        healthConfig.Timeout.String(),
			"max_failures":   healthConfig.MaxFailures,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	}

	_ = json.NewEncoder(w).Encode(response)

	log.WithFields(logrus.Fields{
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
