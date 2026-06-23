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

const (
	logKeyService               = "service"
	logKeyNamespace             = "namespace"
	logKeyPod                   = "pod"
	logKeyMessage               = "message"
	logKeyCategory              = "category"
	logKeyLocalIP               = "local_ip"
	logKeyLocalPort             = "local_port"
	logKeyRemotePort            = "remote_port"
	logKeyAttempt               = "attempt"
	logKeyError                 = "error"
	logValueConnectionLifecycle = "connection_lifecycle"
	logValuePortForwardError    = "port_forward_error"
	logValueConnectionReset     = "connection_reset"
)

// filteredErrorWriter filters out noisy connection reset errors from port-forward output.
type filteredErrorWriter struct {
	namespace string
	pod       string
}

func (w *filteredErrorWriter) Write(p []byte) (int, error) {
	msg := string(p)

	// Filter out noisy connection reset errors that are normal network behavior
	// Check these FIRST before checking for "Unhandled Error" since the message
	// might contain both patterns
	if strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "write: broken pipe") ||
		strings.Contains(msg, "read: connection reset by peer") {
		// Log these as debug level instead of error
		logger.LogDebug("Port-forward connection closed by client", logrus.Fields{
			logKeyNamespace: w.namespace,
			logKeyPod:       w.pod,
			logKeyMessage:   strings.TrimSpace(msg),
			logKeyCategory:  logValueConnectionLifecycle,
		})
		return len(p), nil
	}

	// For other errors that don't match connection reset patterns,
	// log them as warnings but with more context
	if strings.Contains(msg, "Unhandled Error") || strings.Contains(msg, "error copying") {
		// Double-check it's not a connection reset error we missed
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "connection reset") ||
			strings.Contains(lowerMsg, "broken pipe") {
			// Still a connection reset, just log as debug
			logger.LogDebug("Port-forward connection closed", logrus.Fields{
				logKeyNamespace: w.namespace,
				logKeyPod:       w.pod,
				logKeyMessage:   strings.TrimSpace(msg),
				logKeyCategory:  logValueConnectionLifecycle,
			})
			return len(p), nil
		}

		// It's a real error, log as warning
		logger.Log.WithFields(logrus.Fields{
			logKeyNamespace: w.namespace,
			logKeyPod:       w.pod,
			logKeyMessage:   strings.TrimSpace(msg),
			logKeyCategory:  logValuePortForwardError,
		}).Warn("Port-forward stream error")
		return len(p), nil
	}

	// For all other messages, pass through to stdout with context
	logger.LogDebug("Port-forward output", logrus.Fields{
		logKeyNamespace: w.namespace,
		logKeyPod:       w.pod,
		logKeyMessage:   strings.TrimSpace(msg),
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
				logKeyLocalIP: localIP,
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

// runPortForwardWithRetryGeneric runs a port forwarder with retry logic.
// entityLabel is the log field key (e.g., "pod" or "service") and value for the entity being forwarded.
// onReady is called after the port-forward tunnel is established to begin monitoring.
func runPortForwardWithRetryGeneric(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, entity, localIP string,
	localPort, remotePort int,
	entityLabel string,
	establishedMsg string,
	onReady func(context.Context, *PortForwarderSetup, string, string, string, int, int),
) error {
	maxRetries := 15
	retryDelay := 200 * time.Millisecond
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		done := make(chan error, 1)
		go func() {
			err := setup.ForwardPorts.ForwardPorts()
			if err != nil {
				if !isConnectionResetError(err) {
					logger.Log.WithFields(logrus.Fields{
						logKeyNamespace:  namespace,
						entityLabel:      entity,
						logKeyLocalIP:    localIP,
						logKeyLocalPort:  localPort,
						logKeyRemotePort: remotePort,
						logKeyAttempt:    attempt,
						logKeyError:      err.Error(),
					}).Warn(entityLabel + " port-forward ended with error")
				} else {
					logger.LogDebug(entityLabel+" port-forward connection closed", logrus.Fields{
						logKeyNamespace:  namespace,
						entityLabel:      entity,
						logKeyLocalIP:    localIP,
						logKeyLocalPort:  localPort,
						logKeyRemotePort: remotePort,
						logKeyAttempt:    attempt,
						"reason":         logValueConnectionReset,
					})
				}
			}
			done <- err
		}()

		select {
		case <-setup.ReadyChan:
			logger.Log.WithFields(logrus.Fields{
				logKeyNamespace:  namespace,
				entityLabel:      entity,
				logKeyLocalIP:    localIP,
				logKeyLocalPort:  localPort,
				logKeyRemotePort: remotePort,
				logKeyAttempt:    attempt,
			}).Info(establishedMsg)

			onReady(ctx, setup, namespace, entity, localIP, localPort, remotePort)
			return nil
		case err := <-done:
			lastErr = err
			if err != nil && attempt < maxRetries {
				logger.Log.WithFields(logrus.Fields{
					logKeyNamespace:  namespace,
					entityLabel:      entity,
					logKeyLocalIP:    localIP,
					logKeyLocalPort:  localPort,
					logKeyRemotePort: remotePort,
					logKeyAttempt:    attempt,
					logKeyError:      err.Error(),
				}).Warn(entityLabel + " port-forward error detected, retrying immediately...")

				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf(
				"%s port-forward failed after %d attempts: %w",
				entityLabel,
				attempt,
				err,
			)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

// runPortForwardWithRetry runs the port forwarder with retry logic for pods.
func runPortForwardWithRetry(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, pod, localIP string,
	localPort, remotePort int,
) error {
	return runPortForwardWithRetryGeneric(
		ctx, setup, namespace, pod, localIP, localPort, remotePort,
		"pod",
		"🔗 Port-forward tunnel established",
		func(ctx context.Context, s *PortForwarderSetup, ns, ent, lip string, lp, rp int) {
			//nolint:gosec // G118 - context is managed via ctx.Done() inside the goroutine
			go monitorPortForwardAggressively(ctx, s, ns, ent, lip, lp, rp)
		},
	)
}

// monitorPortForwardAggressively monitors the port-forward and restarts it immediately on failure.
func monitorPortForwardAggressively(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, pod, localIP string,
	localPort, remotePort int,
) {
	// Wait for the port-forward to complete
	<-ctx.Done()

	// If context is cancelled, don't restart
	if ctx.Err() != nil {
		return
	}

	// Immediately restart the port-forward
	logger.Log.WithFields(logrus.Fields{
		logKeyNamespace:  namespace,
		logKeyPod:        pod,
		logKeyLocalIP:    localIP,
		logKeyLocalPort:  localPort,
		logKeyRemotePort: remotePort,
	}).Warn("🚨 Port-forward tunnel failed, restarting immediately...")

	// Create a new context for the restart with very short timeout

	restartCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to restart the port-forward immediately
	if err := runPortForwardWithRetry(
		restartCtx,
		setup,
		namespace,
		pod,
		localIP,
		localPort,
		remotePort,
	); err != nil {
		logger.Log.WithFields(logrus.Fields{
			logKeyNamespace:  namespace,
			logKeyPod:        pod,
			logKeyLocalIP:    localIP,
			logKeyLocalPort:  localPort,
			logKeyRemotePort: remotePort,
			logKeyError:      err.Error(),
		}).Error("Failed to restart port-forward immediately")

		// Try one more time with a longer timeout

		restartCtx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()

		if err2 := runPortForwardWithRetry(
			restartCtx2,
			setup,
			namespace,
			pod,
			localIP,
			localPort,
			remotePort,
		); err2 != nil {
			logger.Log.WithFields(logrus.Fields{
				logKeyNamespace:  namespace,
				logKeyPod:        pod,
				logKeyLocalIP:    localIP,
				logKeyLocalPort:  localPort,
				logKeyRemotePort: remotePort,
				logKeyError:      err2.Error(),
			}).Error("Failed to restart port-forward after multiple attempts")
		}
	}
}

// createPersistentPortForwarder creates

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

// StartServicePortForwardOnIP starts a port forward to a Kubernetes service (not a specific pod).
// This is useful for services with Istio sidecar proxies where direct pod access is blocked.
func StartServicePortForwardOnIP(
	ctx context.Context,
	config *rest.Config,
	namespace, service, localIP string,
	localPort, remotePort int,
) error {
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	hostIP := strings.TrimPrefix(config.Host, "https://")
	url := &url.URL{
		Scheme: "https",
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/services/%s/portforward", namespace, service),
		Host:   hostIP,
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}

	out := new(bytes.Buffer)
	errOut := &filteredErrorWriter{
		namespace: namespace,
		pod:       service, // Use service name in logs
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
		logger.LogDebug(
			"NewOnAddresses not available, using standard port forwarding",
			logrus.Fields{
				logKeyLocalIP: localIP,
			},
		)

		fw, err = portforward.New(dialer, ports, ctx.Done(), readyChan, out, errOut)
		if err != nil {
			return fmt.Errorf("failed to create service port-forwarder: %w", err)
		}

		go startFallbackListener(ctx, localIP, localPort)
	}

	setup := &PortForwarderSetup{
		ForwardPorts: fw,
		ReadyChan:    readyChan,
	}

	return runServicePortForwardWithRetry(
		ctx,
		setup,
		namespace,
		service,
		localIP,
		localPort,
		remotePort,
	)
}

// runServicePortForwardWithRetry runs the service port forwarder with retry logic.
func runServicePortForwardWithRetry(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, service, localIP string,
	localPort, remotePort int,
) error {
	return runPortForwardWithRetryGeneric(
		ctx, setup, namespace, service, localIP, localPort, remotePort,
		"service",
		"🔗 Service port-forward tunnel established",
		func(ctx context.Context, s *PortForwarderSetup, ns, ent, lip string, lp, rp int) {
			//nolint:gosec // G118 - context is managed via ctx.Done() inside the goroutine
			go monitorServicePortForward(ctx, s, ns, ent, lip, lp, rp)
		},
	)
}

// monitorServicePortForward monitors the service port-forward and restarts it on failure.
func monitorServicePortForward(
	ctx context.Context,
	setup *PortForwarderSetup,
	namespace, service, localIP string,
	localPort, remotePort int,
) {
	<-ctx.Done()

	if ctx.Err() != nil {
		return
	}

	logger.Log.WithFields(logrus.Fields{
		logKeyNamespace:  namespace,
		logKeyService:    service,
		logKeyLocalIP:    localIP,
		logKeyLocalPort:  localPort,
		logKeyRemotePort: remotePort,
	}).Warn("🚨 Service port-forward tunnel failed, restarting immediately...")

	restartCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runServicePortForwardWithRetry(
		restartCtx,
		setup,
		namespace,
		service,
		localIP,
		localPort,
		remotePort,
	); err != nil {
		logger.Log.WithFields(logrus.Fields{
			logKeyNamespace:  namespace,
			logKeyService:    service,
			logKeyLocalIP:    localIP,
			logKeyLocalPort:  localPort,
			logKeyRemotePort: remotePort,
			logKeyError:      err.Error(),
		}).Error("Failed to restart service port-forward immediately")
	}
}
