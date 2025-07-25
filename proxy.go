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

// responseWriterWrapper wraps http.ResponseWriter to capture metrics
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
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(data)
	w.responseSize += int64(n)
	return n, err
}

// isGRPCRequest checks if the request is a gRPC request
func isGRPCRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	// Check for gRPC content types and also check for gRPC-specific headers
	return strings.HasPrefix(contentType, "application/grpc") ||
		r.Header.Get("grpc-encoding") != "" ||
		r.Header.Get("grpc-accept-encoding") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "grpc")
}

// getProtocolInfo returns detailed protocol information
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

// createTransport creates an optimized transport for the target protocol
func createTransport(isHTTPS bool, protocol string) http.RoundTripper {
	if protocol == "h2c" {
		return &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		}
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     false,
	}

	if isHTTPS {
		transport.TLSClientConfig = loadTLSConfig()
	}

	return transport
}

// getRetryConfig returns retry configuration from environment variables
func getRetryConfig() (maxRetries int, baseDelay time.Duration) {
	maxRetries = 3
	baseDelay = 200 * time.Millisecond

	if envRetries := os.Getenv("PROXY_MAX_RETRIES"); envRetries != "" {
		if parsed, err := strconv.Atoi(envRetries); err == nil && parsed >= 0 && parsed <= 10 {
			maxRetries = parsed
		}
	}

	if envDelay := os.Getenv("PROXY_RETRY_DELAY_MS"); envDelay != "" {
		if parsed, err := strconv.Atoi(envDelay); err == nil && parsed >= 50 && parsed <= 5000 {
			baseDelay = time.Duration(parsed) * time.Millisecond
		}
	}

	return maxRetries, baseDelay
}

// retryableTransport wraps a transport with retry logic for connection failures
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
				// Exponential backoff
				rt.baseDelay*time.Duration(1<<uint(attempt)),
				// Cap at 5 seconds
				5*time.Second)

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

// healthCheckBackend performs a quick health check on the backend service
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

// protocolFallbackTransport tries different protocols on failure
type protocolFallbackTransport struct {
	port       int32
	isGRPC     bool
	maxRetries int
	baseDelay  time.Duration
}

func (pft *protocolFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

// createProtocolFallbackTransport creates a transport that tries multiple protocols
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

	// Add timeout context for the entire request
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	// Get request size
	requestSize := max(r.ContentLength, 0)

	LogRequest(r.Method, r.URL.Path, protocolInfo, r.RemoteAddr)
	LogRequestStart(r.Method, r.URL.Path, isGRPC, requestSize)

	// Set CORS headers for browser compatibility
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, grpc-timeout, grpc-encoding, grpc-accept-encoding")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
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
			w.Header().Set("grpc-status", "3") // INVALID_ARGUMENT
			w.Header().Set("grpc-message", "Invalid host format")
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
			w.Header().Set("grpc-status", "14") // UNAVAILABLE
			w.Header().Set("grpc-message", "Service Unavailable")
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

	// Skip health check for gRPC services to avoid protocol detection errors
	if !isGRPC {
		LogDebug("Checking backend health", logrus.Fields{
			"port": localPort,
		})

		maxHealthRetries := 3
		for attempt := range maxHealthRetries {
			if err := healthCheckBackend(localPort, 1*time.Second); err == nil {
				LogBackendHealth(localPort, "healthy")
				break
			} else if attempt < maxHealthRetries-1 {
				delay := time.Duration(attempt+1) * 300 * time.Millisecond
				LogRetry(attempt+1, delay.String(), err)
				time.Sleep(delay)
			} else {
				LogBackendHealth(localPort, "unhealthy")
			}
		}
	} else {
		LogDebug("Skipping health check for gRPC service", logrus.Fields{
			"port": localPort,
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
				if req.Header.Get("grpc-timeout") == "" {
					req.Header.Set("grpc-timeout", "30S") // 30 second default timeout
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
				if resp.Header.Get("grpc-status") == "" && resp.StatusCode == 200 {
					resp.Header.Set("grpc-status", "0") // OK
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
				w.Header().Set("grpc-status", grpcStatus)
				w.Header().Set("grpc-message", fmt.Sprintf("Proxy Error: %v", err))
				w.WriteHeader(http.StatusOK)
			} else {
				http.Error(w, fmt.Sprintf("Bad Gateway: %v", err), http.StatusBadGateway)
			}
		},
		FlushInterval: time.Millisecond * 100,
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
	LogResponseMetrics(r.Method, r.URL.Path, responseWriter.statusCode, totalDuration, responseWriter.responseSize, isGRPC)
}

// healthCheckHandler provides a simple health check endpoint
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

// servicesHandler provides information about discovered services
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

// loadTLSConfig loads TLS configuration with proper cipher suites for HTTP/2
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
