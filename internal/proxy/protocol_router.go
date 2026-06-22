package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// ProtocolRouter routes incoming connections to either HTTP or TCP/UDP handlers
// based on protocol detection. This is the integration point between the HTTP
// server and the TCP/UDP proxy layer.
type ProtocolRouter struct {
	httpHandler http.Handler
	tcpProxy    *TCPProxy
	udpProxy    *UDPProxy
	detector    *ProtocolDetector
	tcpEnabled  bool
	udpEnabled  bool
}

// NewProtocolRouter creates a new protocol router.
func NewProtocolRouter(
	httpHandler http.Handler,
	tcpProxy *TCPProxy,
	udpProxy *UDPProxy,
	tcpEnabled bool,
	udpEnabled bool,
) *ProtocolRouter {
	return &ProtocolRouter{
		httpHandler: httpHandler,
		tcpProxy:    tcpProxy,
		udpProxy:    udpProxy,
		detector:    NewProtocolDetector(),
		tcpEnabled:  tcpEnabled,
		udpEnabled:  udpEnabled,
	}
}

// ServeHTTP implements http.Handler and routes requests based on protocol
// Note: This is called for HTTP requests only. Non-HTTP protocols would need
// to be handled at the listener level (see listener_adapter.go for reference).
// For now, this ensures HTTP/gRPC/HTTPS continue to work as before.
func (pr *ProtocolRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// All requests coming here are already HTTP
	// Protocol detection for TCP/UDP happens at the listener level
	pr.httpHandler.ServeHTTP(w, r)
}

// HandleConnection handles a raw connection and routes based on protocol.
// This should be called at the listener level for each incoming connection.
// Note: This is a reference implementation. The actual integration depends
// on how the HTTP server's listener is wrapped.
func (pr *ProtocolRouter) HandleConnection(conn net.Conn) error {
	defer func() { _ = conn.Close() }()

	if !pr.tcpEnabled {
		// TCP/UDP disabled, all connections are HTTP
		// This shouldn't happen - HTTP server handles these
		logger.LogError("Non-HTTP connection received but TCP proxy disabled", nil)
		return nil
	}

	// Detect protocol
	protocol, reader, err := pr.detector.DetectFromConnection(conn)
	if err != nil {
		logger.LogError("Protocol detection error", err)
		return err
	}

	// Wrap connection with peeked data
	// Detector always returns a non-nil reader.
	conn = &connWithReader{
		Conn:   conn,
		reader: reader,
	}

	// Route based on protocol
	if IsHTTPProtocol(protocol) {
		// This shouldn't happen - HTTP requests should be handled by HTTP server
		logger.LogError("HTTP protocol detected in raw connection handler", nil)
		return nil
	}

	// Handle TCP connection
	if protocol == ProtocolTCP {
		if pr.tcpProxy == nil {
			logger.LogError("TCP proxy not available", nil)
			return errors.New("tcp proxy not configured")
		}

		// Extract service/namespace from TCP connection
		service, namespace, err := pr.extractServiceFromTCPConnection(conn)
		if err != nil {
			logger.LogError("Failed to extract service info from TCP connection", err)
			return fmt.Errorf("service extraction failed: %w", err)
		}

		// Extract port hint if available
		localPort := 0 // Could extract from SNI or headers

		// Forward the connection
		return pr.tcpProxy.ForwardConnection(conn, service, namespace, localPort)
	}

	// Handle UDP connection
	if protocol == ProtocolUDP {
		if !pr.udpEnabled || pr.udpProxy == nil {
			logger.LogError("UDP not enabled or proxy not available", nil)
			return errors.New("udp proxy not configured")
		}

		logger.LogError("UDP connections in TCP stream unsupported", nil)
		return errors.New("udp in tcp stream unsupported")
	}

	logger.LogError(fmt.Sprintf("Unknown protocol: %s", protocol), nil)
	return fmt.Errorf("unknown protocol: %s", protocol)
}

// extractServiceFromTCPConnection extracts service and namespace from a TCP connection
// Uses SNI for TLS connections or custom headers for non-TLS.
func (pr *ProtocolRouter) extractServiceFromTCPConnection(_ net.Conn) (string, string, error) {
	// TODO: Implement SNI extraction for TLS connections
	// For TLS connections, the ServerName would be in the TLS handshake
	// This would require wrapping the connection with a TLS listener

	// For non-TLS TCP, look for custom headers if the protocol allows it
	// This is application-specific and would depend on the client implementation

	// Default fallback
	service := defaultService
	namespace := defaultNamespace

	// Try to extract from connection metadata if available
	// This is a placeholder for future implementation

	return service, namespace, nil
}
