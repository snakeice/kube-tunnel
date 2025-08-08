package k8s

import (
	"bytes"
	"context"
	"fmt"
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
	localPort, remotePort int32,
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
	fw, err := portforward.New(dialer, ports, ctx.Done(), readyChan, out, errOut)
	if err != nil {
		return fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	// Start port forwarding in a goroutine
	go func() {
		if err := fw.ForwardPorts(); err != nil {
			logger.LogDebug("Port-forward ended", logrus.Fields{
				"namespace": namespace,
				"pod":       pod,
				"error":     err.Error(),
			})
		}
	}()

	// Wait for port-forward to be ready
	select {
	case <-readyChan:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
