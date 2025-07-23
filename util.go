package main

import (
	"fmt"
	"log"
	"net"
	"strings"
)

func parseHost(host string) (service, namespace string, err error) {
	log.Printf("Parsing host: %s", host)

	parts := strings.Split(host, ".")
	if len(parts) < 5 || parts[2] != "svc" {
		log.Printf("Invalid host format: %s (expected format: service.namespace.svc.cluster.local)", host)
		return "", "", fmt.Errorf("invalid host: %s", host)
	}

	service, namespace = parts[0], parts[1]
	log.Printf("Parsed host successfully: service=%s, namespace=%s", service, namespace)
	return service, namespace, nil
}

func getFreePort() (int, error) {
	log.Printf("Looking for free port")

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Printf("Failed to find free port: %v", err)
		return 0, err
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	log.Printf("Found free port: %d", port)
	return port, nil
}
