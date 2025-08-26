package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// filteredErrorWriter filters out noisy connection reset errors from port-forward output.
type filteredErrorWriter struct {
	namespace string
	pod       string
}

func (w *filteredErrorWriter) Write(p []byte) (int, error) {
	msg := string(p)

	// Filter out noisy connection reset errors that are normal network behavior
	if strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "write: broken pipe") {
		// Log these as debug level instead of error
		logger.LogDebug("Port-forward connection closed by client", logrus.Fields{
			"namespace": w.namespace,
			"pod":       w.pod,
			"message":   strings.TrimSpace(msg),
			"category":  "connection_lifecycle",
		})
		return len(p), nil
	}

	// For other errors, log them as warnings but with more context
	if strings.Contains(msg, "Unhandled Error") || strings.Contains(msg, "error copying") {
		logger.Log.WithFields(logrus.Fields{
			"namespace": w.namespace,
			"pod":       w.pod,
			"message":   strings.TrimSpace(msg),
			"category":  "port_forward_error",
		}).Warn("Port-forward stream error")
		return len(p), nil
	}

	// For all other messages, pass through to stdout with context
	logger.LogDebug("Port-forward output", logrus.Fields{
		"namespace": w.namespace,
		"pod":       w.pod,
		"message":   strings.TrimSpace(msg),
	})

	return len(p), nil
}

// PortForwarderSetup contains the port forwarder and ready channel.
type PortForwarderSetup struct {
	ForwardPorts *portforward.PortForwarder
	ReadyChan    chan struct{}
}

func StartPortForwardOnIP(
	ctx context.Context,
	config *rest.Config,
	namespace, pod, localIP string,
	localPort, remotePort int,
) error {
	setup, err := createPortForwarder(ctx, config, namespace, pod, localIP, localPort, remotePort)
	if err != nil {
		return err
	}

	return runPortForwardWithRetry(ctx, setup, namespace, pod, localIP, localPort, remotePort)
}

// createPortForwarder creates and configures the port forwarder.
func createPortForwarder(
	ctx context.Context,
	config *rest.Config,
	namespace, pod, localIP string,
	localPort, remotePort int,
) (*PortForwarderSetup, error) {
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	hostIP := strings.TrimPrefix(config.Host, "https://")
	url := &url.URL{
		Scheme: "https",
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, pod),
		Host:   hostIP,
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}

	out := new(bytes.Buffer)
	// Use custom error writer to filter noisy connection reset errors
	errOut := &filteredErrorWriter{
		namespace: namespace,
		pod:       pod,
	}

	readyChan := make(chan struct{})

	fw, err := portforward.NewOnAddresses(
		dialer,
		[]string{localIP},
		ports,
		ctx.Done(),
		readyChan,
		out,
		errOut,
	)
	if err != nil {
		// Fallback to standard port forwarding if NewOnAddresses is not available
		logger.LogDebug(
			"NewOnAddresses not available, using standard port forwarding",
			logrus.Fields{
				"local_ip": localIP,
			},
		)

		fw, err = portforward.New(dialer, ports, ctx.Done(), readyChan, out, errOut)
		if err != nil {
			return nil, fmt.Errorf("failed to create port-forwarder: %w", err)
		}

		// For standard port forwarding, we need to create a listener on the specific IP
		go startFallbackListener(ctx, localIP, localPort)
	}

	return &PortForwarderSetup{
		ForwardPorts: fw,
		ReadyChan:    readyChan,
	}, nil
}

// runPortForwardWithRetry runs the port forwarder with retry logic.
func runPortForwardWithRetry(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, pod, localIP string,
	localPort, remotePort int,
) error {
	// Retry logic for transient errors
	maxRetries := 5               // Increased retries for better resilience
	retryDelay := 1 * time.Second // Faster initial retry
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		done := make(chan error, 1)
		go func() {
			err := setup.ForwardPorts.ForwardPorts()
			if err != nil {
				// Only log significant errors, filter out connection resets
				if !isConnectionResetError(err) {
					logger.Log.WithFields(logrus.Fields{
						"namespace":   namespace,
						"pod":         pod,
						"local_ip":    localIP,
						"local_port":  localPort,
						"remote_port": remotePort,
						"attempt":     attempt,
						"error":       err.Error(),
					}).Warn("Port-forward ended with error")
				} else {
					logger.LogDebug("Port-forward connection closed", logrus.Fields{
						"namespace":   namespace,
						"pod":         pod,
						"local_ip":    localIP,
						"local_port":  localPort,
						"remote_port": remotePort,
						"attempt":     attempt,
						"reason":      "connection_reset",
					})
				}
			}
			done <- err
		}()

		select {
		case <-setup.ReadyChan:
			logger.Log.WithFields(logrus.Fields{
				"namespace":   namespace,
				"pod":         pod,
				"local_ip":    localIP,
				"local_port":  localPort,
				"remote_port": remotePort,
				"attempt":     attempt,
			}).Info("🔗 Port-forward tunnel established")
			return nil
		case err := <-done:
			lastErr = err
			// Retry only on transient errors
			if err != nil && isTransientPortForwardError(err) && attempt < maxRetries {
				logger.Log.WithFields(logrus.Fields{
					"namespace":   namespace,
					"pod":         pod,
					"local_ip":    localIP,
					"local_port":  localPort,
					"remote_port": remotePort,
					"attempt":     attempt,
					"error":       err.Error(),
				}).Warn("Transient error detected, retrying port-forward...")

				// Exponential backoff with jitter
				delay := time.Duration(attempt) * retryDelay
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
				time.Sleep(delay)
				continue
			}
			// Non-transient or max retries reached
			return fmt.Errorf("port-forward failed after %d attempts: %w", attempt, err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

// startFallbackListener creates a listener on the specific IP for fallback port forwarding.
func startFallbackListener(ctx context.Context, localIP string, localPort int) {
	listener, listenErr := net.Listen("tcp", fmt.Sprintf("%s:%d", localIP, localPort))
	if listenErr != nil {
		logger.LogError("Failed to create listener on specific IP", listenErr)
		return
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			logger.LogError("Failed to close listener", closeErr)
		}
	}()

	// Accept connections and forward them to localhost
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set a deadline for Accept to prevent blocking indefinitely
			if tcpListener, ok := listener.(*net.TCPListener); ok {
				if err := tcpListener.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
					logger.LogDebug("Failed to set deadline on listener", logrus.Fields{
						"error": err.Error(),
					})
				}
			}

			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				// Check if it's a timeout error - if so, continue the loop
				var netErr net.Error
				if errors.As(acceptErr, &netErr) {
					continue
				}
				// For other errors, also continue
				continue
			}

			go handleFallbackConnection(conn, localPort)
		}
	}
}

// handleFallbackConnection handles a single connection in fallback mode.
func handleFallbackConnection(conn net.Conn, localPort int) {
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			logger.LogError("Failed to close connection", closeErr)
		}
	}()

	localConn, dialErr := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if dialErr != nil {
		return
	}
	defer func() {
		if closeErr := localConn.Close(); closeErr != nil {
			logger.LogError("Failed to close local connection", closeErr)
		}
	}()

	// Bidirectional copy
	go copyData(conn, localConn)
	copyData(localConn, conn)
}

// copyData copies data from src to dst in a loop.
func copyData(dst, src net.Conn) {
	defer func() {
		if closeErr := dst.Close(); closeErr != nil {
			logger.LogError("Failed to close destination connection in copy goroutine", closeErr)
		}
		if closeErr := src.Close(); closeErr != nil {
			logger.LogError("Failed to close source connection in copy goroutine", closeErr)
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

// isTransientPortForwardError checks if the error is transient and worth retrying.
func isTransientPortForwardError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Add more patterns as needed
	if strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "connection refused") {
		return true
	}
	return false
}

// isConnectionResetError checks if the error is a connection reset that should be logged quietly.
func isConnectionResetError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "write: broken pipe")
}
