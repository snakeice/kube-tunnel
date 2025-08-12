package tools

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

func ParseHost(host string) (string, string, error) {
	parts := strings.Split(host, ".")
	if len(parts) < 5 || parts[2] != "svc" {
		return "", "", fmt.Errorf("invalid host: %s", host)
	}

	return parts[0], parts[1], nil
}

func GetFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to get free port: %w", err)
	}

	defer func() {
		if err := listener.Close(); err != nil {
			logger.Log.WithError(err).Debug("Failed to close listener")
		}
	}()

	addr := listener.Addr()
	if addr == nil {
		return 0, errors.New("listener address is nil")
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener address is not TCPAddr: %T", addr)
	}
	port := tcpAddr.Port
	if port > 65535 {
		return 0, fmt.Errorf("invalid port number %d", port)
	}
	return port, nil
}
