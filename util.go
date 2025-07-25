package main

import (
	"fmt"
	"net"
	"strings"
)

func parseHost(host string) (service, namespace string, err error) {
	parts := strings.Split(host, ".")
	if len(parts) < 5 || parts[2] != "svc" {
		return "", "", fmt.Errorf("invalid host: %s", host)
	}

	service, namespace = parts[0], parts[1]
	return service, namespace, nil
}

func getFreePort() (int32, error) {

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to get free port: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	return int32(port), nil
}
