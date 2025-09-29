package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type retryableTransport struct {
	base       http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
	isGRPC     bool
}

func (rt *retryableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error

	logger.LogRetryAttempt(0, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)

	retryCtx, retryCancel := rt.createRetryContext(req.Context())
	defer retryCancel()

	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		rt.logAttempt(req, attempt)

		if err := rt.checkDeadline(retryCtx, req, attempt); err != nil {
			return nil, err
		}

		reqClone := req.Clone(retryCtx)

		if attempt > 0 {
			logger.LogRetryAttempt(attempt, rt.maxRetries, req.Method, req.URL.Path, rt.isGRPC)
		}

		resp, err := rt.base.RoundTrip(reqClone)

		if err == nil {
			rt.logSuccess(req, attempt)
			return resp, nil
		}

		if rt.handleError(err, req, attempt) {
			return nil, err
		}

		lastErr = err
		rt.logRetryError(req, attempt, err)

		if attempt < rt.maxRetries {
			if err := rt.waitBeforeRetry(retryCtx, req, attempt); err != nil {
				return nil, err
			}
		}
	}

	logger.LogRetryFailed(rt.maxRetries+1, lastErr)
	return nil, lastErr
}

func (rt *retryableTransport) createRetryContext(
	parentCtx context.Context,
) (context.Context, context.CancelFunc) {
	deadline, hasDeadline := parentCtx.Deadline()

	if hasDeadline {
		return context.WithDeadline(context.Background(), deadline)
	}

	cfg := config.GetConfig()
	return context.WithTimeout(context.Background(), cfg.Performance.ProxyTimeout)
}

func (rt *retryableTransport) logAttempt(req *http.Request, attempt int) {
	logger.LogDebug("Proxy connection attempt", logrus.Fields{
		"method":               req.Method,
		"path":                 req.URL.Path,
		"attempt":              attempt,
		"remote_addr":          req.RemoteAddr,
		"request_id":           req.Header.Get("X-Request-Id"),
		"connection_lifecycle": "open",
	})
}

func (rt *retryableTransport) checkDeadline(
	retryCtx context.Context,
	req *http.Request,
	attempt int,
) error {
	select {
	case <-retryCtx.Done():
		if errors.Is(retryCtx.Err(), context.DeadlineExceeded) {
			logger.LogDebug("Request deadline exceeded during retry", logrus.Fields{
				"method":  req.Method,
				"path":    req.URL.Path,
				"attempt": attempt,
				"grpc":    rt.isGRPC,
			})
			return retryCtx.Err()
		}
	default:
	}
	return nil
}

func (rt *retryableTransport) logSuccess(req *http.Request, attempt int) {
	logger.LogDebug("Proxy connection success", logrus.Fields{
		"method":               req.Method,
		"path":                 req.URL.Path,
		"attempt":              attempt,
		"remote_addr":          req.RemoteAddr,
		"request_id":           req.Header.Get("X-Request-Id"),
		"connection_lifecycle": "success",
	})
}

func (rt *retryableTransport) handleError(
	err error,
	req *http.Request,
	attempt int,
) bool {
	if errors.Is(err, context.Canceled) {
		logger.LogDebug("Request context canceled - not retrying", logrus.Fields{
			"method":               req.Method,
			"path":                 req.URL.Path,
			"attempt":              attempt,
			"grpc":                 rt.isGRPC,
			"remote_addr":          req.RemoteAddr,
			"request_id":           req.Header.Get("X-Request-Id"),
			"connection_lifecycle": "cancel",
		})
		return true
	}

	if errors.Is(err, context.DeadlineExceeded) {
		logger.LogDebug("Request context deadline exceeded - not retrying", logrus.Fields{
			"method":               req.Method,
			"path":                 req.URL.Path,
			"attempt":              attempt,
			"grpc":                 rt.isGRPC,
			"remote_addr":          req.RemoteAddr,
			"request_id":           req.Header.Get("X-Request-Id"),
			"connection_lifecycle": "deadline_exceeded",
		})
		return true
	}

	if !isRetryableError(err) {
		logger.LogNonRetryableError(req.Method, req.URL.Path, err, rt.isGRPC)
		logger.LogDebug("Proxy non-retryable error", logrus.Fields{
			"method":               req.Method,
			"path":                 req.URL.Path,
			"attempt":              attempt,
			"remote_addr":          req.RemoteAddr,
			"request_id":           req.Header.Get("X-Request-Id"),
			"connection_lifecycle": "non-retryable",
			"error":                err.Error(),
		})
		return true
	}

	return false
}

func (rt *retryableTransport) logRetryError(req *http.Request, attempt int, err error) {
	logger.LogDebug("Proxy connection retry", logrus.Fields{
		"method":               req.Method,
		"path":                 req.URL.Path,
		"attempt":              attempt,
		"remote_addr":          req.RemoteAddr,
		"request_id":           req.Header.Get("X-Request-Id"),
		"connection_lifecycle": "retry",
		"error":                err.Error(),
	})
}

func (rt *retryableTransport) waitBeforeRetry(
	retryCtx context.Context,
	req *http.Request,
	attempt int,
) error {
	delay := minDuration(
		rt.baseDelay*time.Duration(1<<attempt),
		1*time.Second)

	logger.LogRetry(attempt+1, delay.String())

	select {
	case <-retryCtx.Done():
		logger.LogDebug("Retry context deadline exceeded during delay", logrus.Fields{
			"method":               req.Method,
			"path":                 req.URL.Path,
			"attempt":              attempt + 1,
			"grpc":                 rt.isGRPC,
			"remote_addr":          req.RemoteAddr,
			"request_id":           req.Header.Get("X-Request-Id"),
			"connection_lifecycle": "retry_deadline_exceeded",
		})
		return retryCtx.Err()
	case <-time.After(delay):
		return nil
	}
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
		transport := createTransport(pft.config, false, protocol, pft.ip, pft.port)
		retryTransport := &retryableTransport{
			base:       transport,
			maxRetries: pft.maxRetries,
			baseDelay:  pft.baseDelay,
			isGRPC:     pft.isGRPC,
		}
		return retryTransport.RoundTrip(req)
	}

	// For non-gRPC requests, only use HTTP/1.1 to ensure proper chunked transfer encoding
	// HTTP/2 doesn't use chunked encoding and can cause issues with large responses
	protocols := []string{"http/1.1"}
	if pft.isGRPC {
		// For gRPC, only use h2c (HTTP/2 cleartext) - never fallback to HTTP/1.1
		protocols = []string{"h2c"}
	}

	var lastErr error

	for _, protocol := range protocols {
		logger.LogDebug("Trying protocol", logrus.Fields{
			"protocol": protocol,
			"grpc":     pft.isGRPC,
			"attempt":  protocol,
		})

		transport := createTransport(pft.config, false, protocol, pft.ip, pft.port)
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

// handleTimeoutResponse writes a timeout response appropriate for the protocol.
func handleTimeoutResponse(w http.ResponseWriter, isGRPC bool) {
	if isGRPC {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "4") // DEADLINE_EXCEEDED
		w.Header().Set("Grpc-Message", "Request timeout")
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "Request timeout", http.StatusGatewayTimeout)
	}
}

// writeErrorResponse writes an error response appropriate for the protocol.
func writeErrorResponse(w http.ResponseWriter, err error, isGRPC bool) {
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

type CustomReverseProxy struct {
	targetIP   string
	targetPort int
	isGRPC     bool
	transport  http.RoundTripper
	bufferPool *bufferPool
	connPool   *connectionPool
}

// Connection Pool Management

type connectionPool struct {
	connections chan net.Conn
	maxSize     int
	targetAddr  string
}

// newConnectionPool creates a new connection pool.
func newConnectionPool(targetIP string, targetPort int, maxSize int) *connectionPool {
	var targetAddr string
	if strings.Contains(targetIP, ":") {
		// IPv6 address - needs brackets
		targetAddr = fmt.Sprintf("[%s]:%d", targetIP, targetPort)
	} else {
		// IPv4 address
		targetAddr = fmt.Sprintf("%s:%d", targetIP, targetPort)
	}

	return &connectionPool{
		connections: make(chan net.Conn, maxSize),
		maxSize:     maxSize,
		targetAddr:  targetAddr,
	}
}

// ServeHTTP implements the http.Handler interface, making it a drop-in replacement for httputil.ReverseProxy.
func (crp *CustomReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Ultra-fast path: skip CORS for internal requests
	if r.Header.Get("X-Internal-Request") == "" && handleCORS(w, r) {
		return
	}

	// Pre-computed target URL format (cached in struct for better performance)
	targetURL := *r.URL
	targetURL.Scheme = "http"
	if strings.Contains(crp.targetIP, ":") {
		targetURL.Host = fmt.Sprintf("[%s]:%d", crp.targetIP, crp.targetPort)
	} else {
		targetURL.Host = fmt.Sprintf("%s:%d", crp.targetIP, crp.targetPort)
	}

	// Minimal request creation
	backendReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		crp.handleError(w, r, fmt.Errorf("failed to create backend request: %w", err))
		return
	}

	// Ultra-fast header copying (skip unnecessary headers)
	crp.copyEssentialHeaders(backendReq, r)

	// Minimal protocol headers
	if crp.isGRPC && backendReq.Header.Get("Content-Type") == "" {
		backendReq.Header.Set("Content-Type", "application/grpc+proto")
	}

	// Execute request immediately
	resp, err := crp.transport.RoundTrip(backendReq)
	if err != nil {
		crp.handleError(w, r, err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.LogError("Failed to close response body", err)
		}
	}()

	// Copy only essential response headers
	crp.copyEssentialResponseHeaders(w, resp)

	// Set status and stream
	w.WriteHeader(resp.StatusCode)
	crp.streamResponseUltraFast(w, resp, r)
}

// copyEssentialHeaders copies only the most essential headers for maximum speed.
func (crp *CustomReverseProxy) copyEssentialHeaders(dst, src *http.Request) {
	// Only copy the most critical headers for functionality
	essentialHeaders := []string{
		"Content-Type",
		"Content-Length",
		"Authorization",
		"User-Agent",
		"Accept",
		"Accept-Encoding",
	}

	for _, header := range essentialHeaders {
		if value := src.Header.Get(header); value != "" {
			dst.Header.Set(header, value)
		}
	}
}

// copyEssentialResponseHeaders copies only essential response headers.
func (crp *CustomReverseProxy) copyEssentialResponseHeaders(
	w http.ResponseWriter,
	resp *http.Response,
) {
	// Only copy essential response headers
	essentialHeaders := []string{
		"Content-Type",
		"Content-Length",
		"Content-Encoding",
		"Cache-Control",
		"Set-Cookie",
	}

	for _, header := range essentialHeaders {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}

	// Copy gRPC headers if needed
	if crp.isGRPC {
		if grpcStatus := resp.Header.Get("Grpc-Status"); grpcStatus != "" {
			w.Header().Set("Grpc-Status", grpcStatus)
		}
		if grpcMessage := resp.Header.Get("Grpc-Message"); grpcMessage != "" {
			w.Header().Set("Grpc-Message", grpcMessage)
		}
	}
}

// streamResponseUltraFast provides the fastest possible response streaming.
func (crp *CustomReverseProxy) streamResponseUltraFast(
	w http.ResponseWriter,
	resp *http.Response,
	r *http.Request,
) {
	// Use the largest possible buffer for maximum throughput
	buffer := make([]byte, 128*1024) // 128KB buffer

	flusher, canFlush := w.(http.Flusher)

	for {
		// Check for client disconnect
		select {
		case <-r.Context().Done():
			return
		default:
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}

			// Only flush for gRPC or every 64KB to reduce syscalls
			if canFlush && (crp.isGRPC || n >= 65536) {
				flusher.Flush()
			}
		}

		if err != nil {
			break
		}
	}

	// Final flush
	if canFlush {
		flusher.Flush()
	}
}

// handleError handles errors during proxying.
func (crp *CustomReverseProxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	// Handle context errors
	if errors.Is(err, context.Canceled) {
		logger.LogDebug("Request canceled", logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
			"grpc":   crp.isGRPC,
		})
		return
	}

	if errors.Is(err, context.DeadlineExceeded) {
		logger.LogDebug("Request timeout", logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
			"grpc":   crp.isGRPC,
		})
		handleTimeoutResponse(w, crp.isGRPC)
		return
	}

	// Log other errors
	logger.LogError("Proxy error", err)

	// Write error response
	writeErrorResponse(w, err, crp.isGRPC)
}

// createReverseProxy configures and returns a new CustomReverseProxy.
func createReverseProxy(
	localIP string,
	localPort int,
	isGRPC bool,
) *CustomReverseProxy {
	// Create appropriate transport based on request type
	var transport http.RoundTripper
	if isGRPC {
		// For gRPC, create h2c (HTTP/2 cleartext) transport
		transport = createH2CTransport(func(network, addr string) (net.Conn, error) {
			var targetAddr string
			if strings.Contains(localIP, ":") {
				targetAddr = fmt.Sprintf("[%s]:%d", localIP, localPort)
			} else {
				targetAddr = fmt.Sprintf("%s:%d", localIP, localPort)
			}
			return net.DialTimeout(network, targetAddr, 1*time.Second)
		})
	} else {
		// For HTTP, use simple transport
		transport = createSimpleTransport(localIP, localPort)
	}

	// Create connection pool for faster requests (max 10 connections)
	connPool := newConnectionPool(localIP, localPort, 10)

	// Create buffer pool
	bufPool := newBufferPool()
	typedBufPool, ok := bufPool.(*bufferPool)
	if !ok {
		// This should never happen, but handle gracefully
		typedBufPool = &bufferPool{
			pool: sync.Pool{
				New: func() any {
					return make([]byte, 256*1024)
				},
			},
		}
	}

	return &CustomReverseProxy{
		targetIP:   localIP,
		targetPort: localPort,
		isGRPC:     isGRPC,
		transport:  transport,
		bufferPool: typedBufPool,
		connPool:   connPool,
	}
}

// Transport Utilities and Helpers

type bufferPool struct {
	pool sync.Pool
}

func newBufferPool() httputil.BufferPool {
	return &bufferPool{
		pool: sync.Pool{
			New: func() any {
				// Use 256KB buffers for large JSON responses
				// This is especially important for chunked transfer encoding
				return make([]byte, 256*1024)
			},
		},
	}
}

func (bp *bufferPool) Get() []byte {
	if buf, ok := bp.pool.Get().([]byte); ok {
		return buf
	}
	// Fallback if type assertion fails
	return make([]byte, 256*1024)
}

func (bp *bufferPool) Put(b []byte) {
	// Reset slice to full capacity before returning to pool
	fullCapSlice := b[:cap(b)]
	bp.pool.Put(&fullCapSlice)
}
