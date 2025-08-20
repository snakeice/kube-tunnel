package proxy

import (
	"context"
	"crypto/tls"
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
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

// NOTE: Legacy global singleton and handlers removed. All request handling
// now goes through the struct in handler.go with injected dependencies.

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

// minDuration returns the smaller of two time.Duration values.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
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
			delay := minDuration(
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

func healthCheckBackendOnIP(ip string, port int, timeout time.Duration) error {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	// Try a simple HEAD request to check if the service is responding
	resp, err := client.Head("http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/")
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
	ip         string
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
func createProtocolFallbackTransport(ip string, port int, isGRPC bool) http.RoundTripper {
	maxRetries, baseDelay := getRetryConfig()

	return &protocolFallbackTransport{
		ip:         ip,
		port:       port,
		isGRPC:     isGRPC,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
		config:     config.GetConfig().Performance,
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
// setupPortForward and checkBackendHealth now live on the Proxy struct (handler.go).

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
func proxyDirector(localIP string, localPort int, isGRPC bool) func(*http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("%s:%d", localIP, localPort)

		if req.Header.Get("Connection") == "" && req.ProtoMajor == 2 {
			req.Header.Del("Connection")
		}
		if deadline, ok := req.Context().Deadline(); ok {
			req.Header.Set("X-Request-Deadline", deadline.Format(time.RFC3339))
		}

		if isGRPC {
			setGRPCHeaders(req)
		}
		logger.LogProxy(req.Method, req.URL.Path, req.Proto, "auto-detect", isGRPC)
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
	localIP string,
	localPort int,
	isGRPC bool,
	w *responseWriterWrapper,
) *httputil.ReverseProxy {
	transport := createProtocolFallbackTransport(localIP, localPort, isGRPC)
	rp := &httputil.ReverseProxy{
		Director:  proxyDirector(localIP, localPort, isGRPC),
		Transport: transport,
		ModifyResponse: proxyModifyResponse( //nolint:bodyclose // handled via wrapper
			w,
			isGRPC,
		),
		ErrorHandler:  proxyErrorHandler(isGRPC),
		FlushInterval: 50 * time.Millisecond,
	}
	rp.ErrorLog = log.New(&reverseProxyLogAdapter{}, "", 0)
	return rp
}

// reverseProxyLogAdapter suppresses noisy reverse proxy errors while retaining debug info.
type reverseProxyLogAdapter struct{}

func (a *reverseProxyLogAdapter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "reverseproxy read error") &&
		strings.Contains(lower, "read on closed response body") {
		logger.LogDebug("Suppressed benign reverse proxy read error", logrus.Fields{"msg": msg})
		return len(p), nil
	}
	if strings.Contains(lower, "reverseproxy read error") &&
		strings.Contains(lower, "connection reset by peer") {
		logger.LogDebug("Upstream connection reset", logrus.Fields{"msg": msg})
		return len(p), nil
	}
	logger.LogDebug("ReverseProxy log", logrus.Fields{"msg": msg})
	return len(p), nil
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
