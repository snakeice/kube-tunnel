package k8s

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

func StartPortForward(
	ctx context.Context,
	config *rest.Config,
	namespace, pod string,
	localPort, remotePort int,
) error {
	return StartPortForwardOnIP(ctx, config, namespace, pod, "127.0.0.1", localPort, remotePort)
}

func StartPortForwardOnIP(
	ctx context.Context,
	config *rest.Config,
	namespace, pod, localIP string,
	localPort, remotePort int,
) error {
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	hostIP := strings.TrimPrefix(config.Host, "https://")
	url := &url.URL{
		Scheme: "https",
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, pod),
		Host:   hostIP,
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}

	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
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
			return fmt.Errorf("failed to create port-forwarder: %w", err)
		}

		// For standard port forwarding, we need to create a listener on the specific IP
		go startFallbackListener(ctx, localIP, localPort)
	}

	// Start port forwarding in a goroutine
	go func() {
		if err := fw.ForwardPorts(); err != nil {
			logger.LogDebug("Port-forward ended", logrus.Fields{
				"namespace": namespace,
				"pod":       pod,
				"local_ip":  localIP,
				"error":     err.Error(),
			})
		}
	}()

	// Wait for port-forward to be ready
	select {
	case <-readyChan:
		logger.LogDebug("Port-forward ready", logrus.Fields{
			"namespace":   namespace,
			"pod":         pod,
			"local_ip":    localIP,
			"local_port":  localPort,
			"remote_port": remotePort,
		})
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				continue
			}

			go handleFallbackConnection(ctx, conn, localPort)
		}
	}
}

// handleFallbackConnection handles a single connection in fallback mode.
func handleFallbackConnection(_ context.Context, conn net.Conn, localPort int) {
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
