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
	if portStr := r.Header.Get("X-Original-Port"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			return port
		}
	}
	return 0
}
