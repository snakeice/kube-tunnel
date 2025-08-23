package tools

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// Global IP manager instance.
var (
	ipOnce            sync.Once  //nolint:gochecknoglobals // global instance
	ipManagerInstance *IPManager //nolint:gochecknoglobals // global instance
)

func ParseHost(host string) (string, string, error) {
	parts := strings.Split(host, ".")
	if len(parts) < 5 || parts[2] != "svc" {
		return "", "", fmt.Errorf("invalid host: %s", host)
	}

	return parts[0], parts[1], nil
}

// IPManager manages allocation of local IP addresses to avoid conflicts.
type IPManager struct {
	mu           sync.RWMutex
	allocatedIPs map[string]bool
	baseIP       string
	currentOctet int
}

// GetIPManager returns the global IP manager instance.
func GetIPManager() *IPManager {
	ipOnce.Do(func() {
		ipManagerInstance = &IPManager{
			allocatedIPs: make(map[string]bool),
			baseIP:       "127.0.0",
			currentOctet: 2, // Start from 127.0.0.2 to avoid localhost
		}
	})
	return ipManagerInstance
}

// GetFreeLocalIP finds an available local IP address in the 127.0.0.x range.
func (im *IPManager) GetFreeLocalIP() (string, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	// Try up to 254 IP addresses (127.0.0.2 to 127.0.0.255)
	for range 254 {
		if im.currentOctet > 255 {
			im.currentOctet = 2 // Wrap around, skip 0 and 1
		}

		candidateIP := fmt.Sprintf("%s.%d", im.baseIP, im.currentOctet)

		// Skip if already allocated
		if im.allocatedIPs[candidateIP] {
			im.currentOctet++
			continue
		}

		// Test if IP is actually available by trying to bind to it
		if im.isIPAvailable(candidateIP) {
			im.allocatedIPs[candidateIP] = true
			logger.Log.Debugf("Allocated local IP: %s", candidateIP)
			im.currentOctet++
			return candidateIP, nil
		}

		im.currentOctet++
	}

	return "", errors.New("no available local IP addresses found in 127.0.0.x range")
}

// ReleaseLocalIP marks an IP address as available for reuse.
func (im *IPManager) ReleaseLocalIP(ip string) {
	im.mu.Lock()
	defer im.mu.Unlock()

	delete(im.allocatedIPs, ip)
	logger.Log.Debugf("Released local IP: %s", ip)
}

// isIPAvailable checks if an IP address is available by attempting to bind to it.
func (im *IPManager) isIPAvailable(ip string) bool {
	// Try to bind to a random port on this IP
	listener, err := net.Listen("tcp", ip+":0")
	if err != nil {
		// If we can't bind, the IP might not be available or configured
		return false
	}

	defer func() {
		if err := listener.Close(); err != nil {
			logger.Log.WithError(err).Debug("Failed to close test listener")
		}
	}()

	return true
}

// GetFreePortOnIP gets a free port on a specific IP address.
func GetFreePortOnIP(ip string) (int, error) {
	listener, err := net.Listen("tcp", ip+":0")
	if err != nil {
		return 0, fmt.Errorf("failed to get free port on IP %s: %w", ip, err)
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

// IsPortAvailableOnIP checks if a specific port is available on a specific IP address.
func IsPortAvailableOnIP(ip string, port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}

	addr := fmt.Sprintf("%s:%d", ip, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}

	defer func() {
		if err := listener.Close(); err != nil {
			logger.Log.WithError(err).Debug("Failed to close test listener")
		}
	}()

	return true
}

// ValidateConnection tests if a connection can be established to an IP:port.
func ValidateConnection(ip string, port int) error {
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err != nil {
		return err
	}

	if err := conn.Close(); err != nil {
		logger.Log.WithError(err).Debug("Failed to close validation connection")
	}

	return nil
}
