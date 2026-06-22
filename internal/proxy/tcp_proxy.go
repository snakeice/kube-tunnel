package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	// TCPBufferSize is the default buffer size for TCP copying.
	TCPBufferSize = 32 * 1024 // 32KB buffer

	// TCPIdleTimeout is the idle timeout for TCP connections.
	TCPIdleTimeout = 5 * time.Minute
)

// TCPProxy handles TCP traffic forwarding.
type TCPProxy struct {
	cache       cache.Cache
	bufferPool  *sync.Pool
	idleTimeout time.Duration
}

// NewTCPProxy creates a new TCP proxy.
func NewTCPProxy(c cache.Cache) *TCPProxy {
	return &TCPProxy{
		cache: c,
		bufferPool: &sync.Pool{
			New: func() any {
				b := make([]byte, TCPBufferSize)
				return &b
			},
		},
		idleTimeout: TCPIdleTimeout,
	}
}

// ForwardConnection forwards a TCP connection to the target service
// service and namespace are extracted from the connection (SNI, headers, etc.)
// localPort is optional - if provided, it's used for port-forward hint.
func (tp *TCPProxy) ForwardConnection(
	clientConn net.Conn,
	service string,
	namespace string,
	localPort int,
) error {
	defer func() {
		if err := clientConn.Close(); err != nil {
			logger.LogError("Failed to close client connection", err)
		}
	}()

	// Set deadlines for connection setup
	if err := clientConn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		logger.LogError("Failed to set client connection deadline", err)
	}
	defer func() {
		if err := clientConn.SetDeadline(time.Time{}); err != nil {
			logger.LogError("Failed to clear client connection deadline", err)
		}
	}() // Clear deadline after setup

	// Get or create port-forward
	var targetIP string
	var targetPort int
	var err error

	if localPort > 0 {
		// Try to match the requested port
		targetIP, targetPort, err = tp.cache.EnsurePortForwardWithHint(
			service,
			namespace,
			localPort,
		)
	} else {
		// Use default port
		targetIP, targetPort, err = tp.cache.EnsurePortForward(service, namespace)
	}

	if err != nil {
		logger.LogError(
			fmt.Sprintf("Failed to setup port-forward for %s.%s", service, namespace),
			err,
		)
		return fmt.Errorf("port-forward setup failed: %w", err)
	}

	// Connect to target
	targetAddr := net.JoinHostPort(targetIP, strconv.Itoa(targetPort))
	targetConn, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		logger.LogError(fmt.Sprintf("Failed to connect to target %s", targetAddr), err)
		return fmt.Errorf("target connection failed: %w", err)
	}
	defer func() {
		if err := targetConn.Close(); err != nil {
			logger.LogError("Failed to close target connection", err)
		}
	}()

	// Clear deadline for actual data transfer
	if err := clientConn.SetDeadline(time.Time{}); err != nil {
		logger.LogError("Failed to clear client connection deadline", err)
	}

	// Set up bidirectional forwarding
	errChan := make(chan error, 2)
	wg := sync.WaitGroup{}

	// Client -> Target
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := tp.copy(targetConn, clientConn)
		if err != nil && !errors.Is(err, io.EOF) {
			logger.LogError("Client->Target copy error", err)
		}
		errChan <- err
	}()

	// Target -> Client
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := tp.copy(clientConn, targetConn)
		if err != nil && !errors.Is(err, io.EOF) {
			logger.LogError("Target->Client copy error", err)
		}
		errChan <- err
	}()

	// Wait for goroutines to finish
	wg.Wait()
	close(errChan)

	return nil
}

// copy performs bidirectional copying from src to dst.
func (tp *TCPProxy) copy(dst io.Writer, src io.Reader) error {
	// Get buffer from pool
	bufAny := tp.bufferPool.Get()
	buf, ok := bufAny.(*[]byte)
	if !ok {
		b := make([]byte, TCPBufferSize)
		buf = &b
	}
	defer tp.bufferPool.Put(buf)

	var written int64
	for {
		// Read from source with timeout
		n, err := src.Read(*buf)
		if n > 0 {
			// Write to destination
			w, errWrite := dst.Write((*buf)[:n])
			written += int64(w)
			if errWrite != nil {
				return errWrite
			}
		}
		if err != nil {
			return err
		}
	}
}

// UDPProxy handles UDP traffic forwarding.
type UDPProxy struct {
	cache      cache.Cache
	bufferPool *sync.Pool
}

// NewUDPProxy creates a new UDP proxy.
func NewUDPProxy(c cache.Cache) *UDPProxy {
	return &UDPProxy{
		cache: c,
		bufferPool: &sync.Pool{
			New: func() any {
				b := make([]byte, TCPBufferSize)
				return &b
			},
		},
	}
}

// ForwardPacket forwards a UDP packet to the target service
// Implements UDP-specific connection tracking if needed.
func (up *UDPProxy) ForwardPacket(
	clientAddr net.Addr,
	data []byte,
	service string,
	namespace string,
	localPort int,
) ([]byte, error) {
	// Get or create port-forward
	var targetIP string
	var targetPort int
	var err error

	if localPort > 0 {
		targetIP, targetPort, err = up.cache.EnsurePortForwardWithHint(
			service,
			namespace,
			localPort,
		)
	} else {
		targetIP, targetPort, err = up.cache.EnsurePortForward(service, namespace)
	}

	if err != nil {
		logger.LogError(
			fmt.Sprintf("Failed to setup port-forward for UDP %s.%s", service, namespace),
			err,
		)
		return nil, fmt.Errorf("port-forward setup failed: %w", err)
	}

	// Create UDP connection to target
	targetAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", targetIP, targetPort))
	if err != nil {
		logger.LogError("Failed to resolve UDP target address", err)
		return nil, fmt.Errorf("invalid target address: %w", err)
	}

	// Create a temporary UDP connection for this packet
	conn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		logger.LogError("Failed to create UDP connection", err)
		return nil, fmt.Errorf("udp connection failed: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.LogError("Failed to close UDP connection", err)
		}
	}()

	// Set read deadline for response
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		logger.LogError("Failed to set UDP read deadline", err)
	}

	// Send packet to target
	if _, err := conn.Write(data); err != nil {
		logger.LogError("Failed to write UDP packet", err)
		return nil, fmt.Errorf("failed to send packet: %w", err)
	}

	// Get buffer and read response
	bufAny := up.bufferPool.Get()
	buf, ok := bufAny.(*[]byte)
	if !ok {
		b := make([]byte, TCPBufferSize)
		buf = &b
	}
	defer up.bufferPool.Put(buf)

	n, err := conn.Read(*buf)
	if err != nil && !errors.Is(err, io.EOF) {
		logger.LogError("Failed to read UDP response", err)
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	if n == 0 {
		return nil, nil
	}

	// Return response (copy to avoid buffer reuse issues)
	response := make([]byte, n)
	copy(response, (*buf)[:n])
	return response, nil
}
