package proxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

func isGRPCRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return strings.HasPrefix(contentType, "application/grpc") ||
		r.Header.Get("Grpc-Encoding") != "" ||
		r.Header.Get("Grpc-Accept-Encoding") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "grpc")
}

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

func handleCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers",
			"Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		w.WriteHeader(http.StatusOK)
		return true
	}
	return false
}

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
			w.Header().Set("Grpc-Status", "3")
			w.Header().Set("Grpc-Message", "Invalid host format")
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Invalid host format", http.StatusBadRequest)
		}
		return "", "", err
	}
	return service, namespace, nil
}

func extractOriginalPort(r *http.Request) int {
	// First try the explicit header
	if portStr := r.Header.Get("X-Original-Port"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			return port
		}
	}

	// Try X-Forwarded-Port header
	if portStr := r.Header.Get("X-Forwarded-Port"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			return port
		}
	}

	// Extract port from Host header (e.g., service.namespace.svc.cluster.local:8080)
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		portStr := host[idx+1:]
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port < 65536 {
			return port
		}
	}

	return 0
}

// isLocalRequest checks if the request is targeting localhost/127.0.0.1
// instead of a Kubernetes service (*.svc.cluster.local).
func isLocalRequest(r *http.Request) bool {
	host := r.Host

	// Remove port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Check for localhost and loopback addresses
	if host == "localhost" ||
		host == "127.0.0.1" ||
		strings.HasPrefix(host, "127.") ||
		host == "::1" ||
		host == "" {
		return true
	}

	// Check if it's NOT a Kubernetes service domain
	// Valid Kubernetes services end with .svc.cluster.local
	if !strings.Contains(host, ".svc.cluster.local") && !strings.Contains(host, ".svc.") {
		// It's not a Kubernetes service, but could be an IP
		// Check if it looks like an IP address
		parts := strings.Split(host, ".")
		if len(parts) == 4 {
			// Might be an IPv4 address - consider it local/non-k8s
			return true
		}
	}

	return false
}
