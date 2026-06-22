package proxy

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

// ProtocolMultiplexer routes incoming connections to appropriate handlers
// based on protocol detection.
type ProtocolMultiplexer struct {
	httpHandler      net.Listener // Underlying HTTP listener
	tcpProxy         *TCPProxy
	udpProxy         *UDPProxy
	protocolDetector *ProtocolDetector
	listener         net.Listener
}

// NewProtocolMultiplexer creates a new protocol multiplexer.
func NewProtocolMultiplexer(
	listener net.Listener,
	tcpProxy *TCPProxy,
	udpProxy *UDPProxy,
) *ProtocolMultiplexer {
	return &ProtocolMultiplexer{
		httpHandler:      listener,
		tcpProxy:         tcpProxy,
		udpProxy:         udpProxy,
		protocolDetector: NewProtocolDetector(),
		listener:         listener,
	}
}

// Accept accepts incoming connections and routes them based on protocol.
func (pm *ProtocolMultiplexer) Accept() (net.Conn, error) {
	// Accept from underlying listener
	conn, err := pm.httpHandler.Accept()
	if err != nil {
		return nil, err
	}

	// Detect protocol
	protocol, reader, err := pm.protocolDetector.DetectFromConnection(conn)
	if err != nil {
		logger.LogError("Protocol detection error", err)
		_ = conn.Close()
		return nil, err
	}

	// Wrap connection with peeked data
	// Detector always returns a non-nil reader.
	conn = &connWithReader{
		Conn:   conn,
		reader: reader,
	}

	// Log protocol detection
	logger.LogDebug(
		fmt.Sprintf("Detected protocol: %s", protocol),
		nil,
	)

	// Route based on protocol
	if IsHTTPProtocol(protocol) {
		// Return connection as-is for HTTP handler
		return conn, nil
	}

	// For TCP/UDP, handle in goroutine and return placeholder
	// This is a bit tricky since net.Listener expects to return connections
	// For non-HTTP protocols, we need to handle them differently
	go pm.handleNonHTTPConnection(conn, protocol)

	// Return next connection from listener
	return pm.Accept()
}

// handleNonHTTPConnection routes non-HTTP connections to appropriate proxy.
func (pm *ProtocolMultiplexer) handleNonHTTPConnection(conn net.Conn, protocol Protocol) {
	defer func() { _ = conn.Close() }()

	switch protocol {
	case ProtocolHTTP, ProtocolTCP:
		// Extract service/namespace from connection
		service, namespace, err := pm.extractServiceInfo(conn)
		if err != nil {
			logger.LogError("Failed to extract service info", err)
			return
		}

		// Get target port hint if available
		localPort := extractPortHint(conn)

		// Forward the connection
		if err := pm.tcpProxy.ForwardConnection(conn, service, namespace, localPort); err != nil {
			logger.LogError(
				fmt.Sprintf("TCP forwarding failed for %s.%s", service, namespace),
				err,
			)
		}

	case ProtocolUDP:
		// UDP is handled differently - it's connectionless
		logger.LogError("UDP in TCP stream detected - UDP should use UDP listener", nil)

	case ProtocolGRPC:
		// gRPC is HTTP/2 based, shouldn't reach non-HTTP path
		logger.LogError(
			"gRPC detected in non-HTTP handler - should be handled by HTTP handler",
			nil,
		)

	default:
		logger.LogError(fmt.Sprintf("Unknown protocol: %s", protocol), nil)
	}
}

// Close closes the underlying listener.
func (pm *ProtocolMultiplexer) Close() error {
	if pm.httpHandler != nil {
		return pm.httpHandler.Close()
	}
	return nil
}

// Addr returns the listener address.
func (pm *ProtocolMultiplexer) Addr() net.Addr {
	if pm.httpHandler != nil {
		return pm.httpHandler.Addr()
	}
	return nil
}

// extractServiceInfo extracts service and namespace from a TCP connection.
func (pm *ProtocolMultiplexer) extractServiceInfo(conn net.Conn) (string, string, error) {
	// Try to get TLS info if available
	_ = conn // Placeholder for future TLS handling

	// Default values
	service := defaultService
	namespace := defaultNamespace

	// Try to extract from custom headers if readable
	service, namespace = extractServiceFromHeaders(conn, service, namespace)

	// Validate extracted values
	if _, _, err := tools.ParseHost(
		fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace),
	); err != nil {
		return "", "", fmt.Errorf("invalid service/namespace: %w", err)
	}

	return service, namespace, nil
}

// extractServiceFromHeaders tries to parse X-Kube-Service headers from the connection.
func extractServiceFromHeaders(conn net.Conn, service, namespace string) (string, string) {
	reader, ok := conn.(interface{ Peek(int) ([]byte, error) })
	if !ok {
		return service, namespace
	}

	peek, err := reader.Peek(512)
	if err != nil {
		return service, namespace
	}

	peekStr := string(peek)

	// Try to extract service and namespace from custom headers
	// Format: X-Kube-Service: service.namespace
	service, namespace = parseXKubeService(peekStr, service, namespace)

	return service, namespace
}

func parseXKubeService(peekStr, service, namespace string) (string, string) {
	idx := strings.Index(peekStr, "X-Kube-Service:")
	if idx == -1 {
		return service, namespace
	}

	lineEnd := strings.Index(peekStr[idx:], "\r\n")
	if lineEnd == -1 {
		return service, namespace
	}

	value := strings.TrimSpace(peekStr[idx+15 : idx+lineEnd])
	parts := strings.Split(value, ".")
	if len(parts) >= 1 {
		service = parts[0]
		if len(parts) >= 2 {
			namespace = parts[1]
		}
	}

	return service, namespace
}

// extractPortHint extracts port number from connection if available.
func extractPortHint(conn net.Conn) int {
	// For TCP connections, we could read headers to find port hints
	// For now, return 0 (use default port)
	// In a production implementation, this would parse custom headers
	return 0
}

// connWithReader wraps a connection with a reader that has peeked data.
type connWithReader struct {
	net.Conn
	reader io.Reader
}

// Read reads from the wrapped reader first, then from the connection.
func (cwr *connWithReader) Read(b []byte) (int, error) {
	return cwr.reader.Read(b)
}
