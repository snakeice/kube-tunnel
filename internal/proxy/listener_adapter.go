package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// TCPListenerAdapter wraps a TCP listener and returns a protocol multiplexer
// that automatically routes HTTP and non-HTTP protocols to their respective handlers.
type TCPListenerAdapter struct {
	listener net.Listener
	tcp      *TCPProxy
	udp      *UDPProxy
	detector *ProtocolDetector
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewTCPListenerAdapter creates a new TCP listener adapter.
func NewTCPListenerAdapter(
	listener net.Listener,
	tcp *TCPProxy,
	udp *UDPProxy,
) *TCPListenerAdapter {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPListenerAdapter{
		listener: listener,
		tcp:      tcp,
		udp:      udp,
		detector: NewProtocolDetector(),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Accept returns connections from the underlying listener
// Note: This is a transparent pass-through that allows the HTTP server
// to handle connections normally. Protocol routing happens at the HTTP
// handler level, not at the listener level.
func (tla *TCPListenerAdapter) Accept() (net.Conn, error) {
	return tla.listener.Accept()
}

// Close closes the underlying listener.
func (tla *TCPListenerAdapter) Close() error {
	tla.cancel()
	tla.wg.Wait()
	return tla.listener.Close()
}

// Addr returns the listener address.
func (tla *TCPListenerAdapter) Addr() net.Addr {
	return tla.listener.Addr()
}

// StartNonHTTPHandler starts a separate listener for non-HTTP protocols
// This approach uses a separate port or goroutine to handle TCP connections
// that are detected as non-HTTP, allowing HTTP and TCP to coexist on the same port.
func (tla *TCPListenerAdapter) StartNonHTTPHandler() error {
	// For a cleaner implementation, non-HTTP protocols can be handled by:
	// 1. Protocol detection at the HTTP handler level (preferred)
	// 2. A separate listener on a different port
	// 3. Connection peeking before HTTP handler
	//
	// Currently implemented at the HTTP handler level via the ProtocolDetector
	logger.Log.Debug("Non-HTTP handler integration ready at HTTP handler level")
	return nil
}

// WrapHandler wraps an HTTP handler with protocol detection
// This is the recommended approach: detect protocol at the handler level
// before passing to the actual HTTP handler.
func (tla *TCPListenerAdapter) WrapHandler(httpHandler func(net.Conn) error) func(net.Conn) error {
	return func(conn net.Conn) error {
		// Detect protocol
		protocol, reader, err := tla.detector.DetectFromConnection(conn)
		if err != nil {
			logger.LogError("Protocol detection failed", err)
			_ = conn.Close()
			return err
		}

		// Wrap connection with peeked data
		// Detector always returns a non-nil reader, so we always wrap.
		conn = &connWithReader{
			Conn:   conn,
			reader: reader,
		}

		// Route based on protocol
		if IsHTTPProtocol(protocol) {
			// Handle as HTTP
			return httpHandler(conn)
		}

		// Handle TCP connections in background
		if tla.tcp != nil {
			go func() {
				service, namespace, err := extractServiceFromTCPConnection(conn)
				if err != nil {
					logger.LogError("Failed to extract service info from TCP connection", err)
					_ = conn.Close()
					return
				}

				localPort := 0 // Could extract from headers if available
				if err := tla.tcp.ForwardConnection(
					conn,
					service,
					namespace,
					localPort,
				); err != nil {
					logger.LogError(
						fmt.Sprintf("TCP forwarding failed for %s.%s", service, namespace),
						err,
					)
				}
			}()
			return nil
		}

		// No TCP proxy available
		logger.LogError("TCP proxy not available but non-HTTP protocol detected", nil)
		_ = conn.Close()
		return errors.New("tcp proxy not configured")
	}
}

// extractServiceFromTCPConnection extracts service name from TCP connection
// Uses SNI (TLS) or custom headers if available.
func extractServiceFromTCPConnection(_ net.Conn) (string, string, error) {
	// For basic implementation, return defaults
	// In production, this would extract from SNI or custom headers
	service := defaultService
	namespace := defaultNamespace

	// TODO: Implement SNI extraction for TLS connections
	// TODO: Implement custom header extraction for non-TLS TCP

	return service, namespace, nil
}

// Note: The recommended approach for kube-tunnel is to integrate
// protocol detection at the HTTP handler level rather than at the
// listener level. This allows:
// 1. HTTP traffic to be handled normally by the existing HTTP server
// 2. Protocol detection to happen before routing
// 3. Non-HTTP protocols to be handled via goroutines
// 4. Backward compatibility with existing code
//
// This approach avoids the complexity of replacing the net.Listener
// and is more maintainable.
