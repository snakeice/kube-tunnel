package proxy

import (
	"bufio"
	"io"
	"net"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// Protocol represents the detected network protocol.
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolGRPC Protocol = "grpc"
	ProtocolTCP  Protocol = "tcp"
	ProtocolUDP  Protocol = "udp"
)

const (
	// peekSize is the number of bytes to peek for protocol detection.
	peekSize = 256
)

const (
	defaultService   = "unknown"
	defaultNamespace = "default"
)

// getHTTPMethods returns common HTTP method names used for request detection.
func getHTTPMethods() []string {
	return []string{
		"GET",
		"POST",
		"PUT",
		"DELETE",
		"HEAD",
		"OPTIONS",
		"PATCH",
		"CONNECT",
		"TRACE",
	}
}

// ProtocolDetector detects the protocol of incoming connections.
type ProtocolDetector struct{}

// NewProtocolDetector creates a new protocol detector.
func NewProtocolDetector() *ProtocolDetector {
	return &ProtocolDetector{}
}

// DetectFromConnection peeks at the incoming connection and detects the protocol
// Returns the detected protocol and a reconstructed reader that includes peeked bytes.
func (pd *ProtocolDetector) DetectFromConnection(conn net.Conn) (Protocol, io.Reader, error) {
	// Create a buffered reader to peek at incoming data
	br := bufio.NewReaderSize(conn, peekSize)

	// Peek at the first few bytes without consuming them
	peek, err := br.Peek(peekSize)
	if err != nil && err != io.EOF {
		logger.LogError("Failed to peek at connection", err)
		// If peek fails, assume TCP (safer fallback)
		return ProtocolTCP, br, nil
	}

	if len(peek) == 0 {
		// No data available, assume TCP
		return ProtocolTCP, br, nil
	}

	// Detect protocol based on first bytes
	protocol := pd.detectProtocol(peek)

	return protocol, br, nil
}

// detectProtocol analyzes the peeked bytes and determines the protocol.
func (pd *ProtocolDetector) detectProtocol(peek []byte) Protocol {
	if len(peek) == 0 {
		return ProtocolTCP // Default to TCP for empty data
	}

	peekStr := string(peek)

	// Check for HTTP methods (HTTP/1.1 or HTTP/1.0)
	for _, method := range getHTTPMethods() {
		if strings.HasPrefix(peekStr, method+" ") {
			return ProtocolHTTP
		}
	}

	// Check for HTTP/2 (h2c starts with PRI * HTTP/2.0)
	if strings.HasPrefix(peekStr, "PRI * HTTP/2.0") {
		return ProtocolHTTP // h2c is HTTP/2 over cleartext
	}

	// Check for TLS (starts with 0x16 for TLS Handshake)
	if len(peek) >= 1 && peek[0] == 0x16 {
		// This is TLS, but we need to determine if it's gRPC or HTTPS
		// For now, classify as HTTP since we'll use TLS ALPN to determine gRPC
		return ProtocolHTTP
	}

	// Default to TCP for any other protocol
	return ProtocolTCP
}

// IsHTTPProtocol returns true if the protocol is HTTP or gRPC (both HTTP-based).
func IsHTTPProtocol(proto Protocol) bool {
	return proto == ProtocolHTTP || proto == ProtocolGRPC
}

// IsTCPProtocol returns true if the protocol is TCP.
func IsTCPProtocol(proto Protocol) bool {
	return proto == ProtocolTCP
}

// IsUDPProtocol returns true if the protocol is UDP.
func IsUDPProtocol(proto Protocol) bool {
	return proto == ProtocolUDP
}

// closeQuietly closes a connection and discards any error.
// This is used for defer/cleanup paths where the error is irrelevant.
func closeQuietly(c net.Conn) {
	_ = c.Close()
}
