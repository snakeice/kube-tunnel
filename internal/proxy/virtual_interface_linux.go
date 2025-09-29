//go:build linux
// +build linux

package proxy

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// LinuxVirtualInterfaceManager manages virtual interfaces on Linux using ip command.
type LinuxVirtualInterfaceManager struct {
	interfaces map[string]*LinuxVirtualInterface
	mu         sync.RWMutex
}

// LinuxVirtualInterface represents a virtual interface on Linux.
type LinuxVirtualInterface struct {
	Name    string // Interface name (e.g., "dummy0", "kube-tun0")
	IP      net.IP // Interface IP address
	Created bool   // Whether this interface was created by us
}

// NewVirtualInterfaceProvider creates a new Linux virtual interface manager.
func NewVirtualInterfaceProvider() VirtualInterfaceProvider {
	return &LinuxVirtualInterfaceManager{
		interfaces: make(map[string]*LinuxVirtualInterface),
	}
}

// IsSupported checks if virtual interface creation is supported on Linux.
func (vim *LinuxVirtualInterfaceManager) IsSupported() bool {
	if runtime.GOOS != osLinux {
		return false
	}

	// Check if ip command is available
	_, err := exec.LookPath("ip")
	return err == nil
}

// CheckRequirements verifies that virtual interface creation can be used on Linux.
func (vim *LinuxVirtualInterfaceManager) CheckRequirements() error {
	if runtime.GOOS != osLinux {
		return errors.New("virtual interface management is only supported on Linux")
	}

	// Check if ip command is available
	_, err := exec.LookPath("ip")
	if err != nil {
		return fmt.Errorf("ip command not found: %w", err)
	}

	// Check if we can run ip command (might require sudo for creating interfaces)
	cmd := exec.Command("ip", "link", "show")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run ip command: %w", err)
	}

	return nil
}

// CreateVirtualInterface creates a virtual interface using ip command on Linux.
func (vim *LinuxVirtualInterfaceManager) CreateVirtualInterface(name, ipStr string) error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	// Parse the IP address
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return fmt.Errorf("invalid IP address: %s", ipStr)
	}

	// Check if interface already exists
	if vim.IsInterfaceExists(name) {
		logger.Log.WithFields(logrus.Fields{
			"interface": name,
			"ip":        ipStr,
		}).Warn("Virtual interface already exists, skipping creation")
		return nil
	}

	// Create the dummy interface
	cmd := exec.Command("sudo", "ip", "link", "add", name, "type", "dummy")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create dummy interface %s: %w", name, err)
	}

	// Set the IP address
	cmd = exec.Command("sudo", "ip", "addr", "add", fmt.Sprintf("%s/32", ipStr), "dev", name)
	if err := cmd.Run(); err != nil {
		// Try to clean up the interface we just created
		if err := vim.cleanupInterface(name); err != nil {
			logger.Log.WithError(err).
				WithField("interface", name).
				Warn("Failed to cleanup virtual interface after IP assignment failure")
		}
		return fmt.Errorf("failed to set IP %s on interface %s: %w", ipStr, name, err)
	}

	// Bring the interface up
	cmd = exec.Command("sudo", "ip", "link", "set", name, "up")
	if err := cmd.Run(); err != nil {
		// Try to clean up the interface we just created
		if err := vim.cleanupInterface(name); err != nil {
			logger.Log.WithError(err).
				WithField("interface", name).
				Warn("Failed to cleanup virtual interface after bringing up failure")
		}
		return fmt.Errorf("failed to bring up interface %s: %w", name, err)
	}

	// Store the interface information
	vim.interfaces[name] = &LinuxVirtualInterface{
		Name:    name,
		IP:      ip,
		Created: true,
	}

	logger.Log.WithFields(logrus.Fields{
		"interface": name,
		"ip":        ipStr,
	}).Info("Created virtual interface")

	return nil
}

// DestroyVirtualInterface removes a virtual interface on Linux.
func (vim *LinuxVirtualInterfaceManager) DestroyVirtualInterface(name string) error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	return vim.cleanupInterface(name)
}

// cleanupInterface removes an interface (internal method).
func (vim *LinuxVirtualInterfaceManager) cleanupInterface(name string) error {
	// Remove the interface
	cmd := exec.Command("sudo", "ip", "link", "delete", name)
	if err := cmd.Run(); err != nil {
		logger.Log.WithFields(logrus.Fields{
			"interface": name,
			"error":     err,
		}).Warn("Failed to remove virtual interface")
		return fmt.Errorf("failed to remove virtual interface %s: %w", name, err)
	}

	// Remove from our tracking
	delete(vim.interfaces, name)

	logger.Log.WithField("interface", name).Info("Removed virtual interface")
	return nil
}

// IsInterfaceExists checks if a network interface exists on Linux.
func (vim *LinuxVirtualInterfaceManager) IsInterfaceExists(name string) bool {
	cmd := exec.Command("ip", "link", "show", name)
	return cmd.Run() == nil
}

// GetInterfaceIP gets the IP address of a network interface on Linux.
func (vim *LinuxVirtualInterfaceManager) GetInterfaceIP(name string) (net.IP, error) {
	cmd := exec.Command("ip", "addr", "show", name)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %s info: %w", name, err)
	}

	// Parse ip addr output to find inet address
	lines := strings.SplitSeq(string(output), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// Remove the /32 suffix if present
				ipStr := strings.Split(parts[1], "/")[0]
				ip := net.ParseIP(ipStr)
				if ip != nil {
					return ip, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no IP address found for interface %s", name)
}

// ListVirtualInterfaces lists all virtual interfaces managed by this provider.
func (vim *LinuxVirtualInterfaceManager) ListVirtualInterfaces() ([]string, error) {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	var interfaces []string
	for name := range vim.interfaces {
		interfaces = append(interfaces, name)
	}
	return interfaces, nil
}

// Cleanup removes all virtual interfaces created by this manager.
func (vim *LinuxVirtualInterfaceManager) Cleanup() error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	var errors []string
	for name := range vim.interfaces {
		if err := vim.cleanupInterface(name); err != nil {
			errors = append(errors, fmt.Sprintf("failed to cleanup interface %s: %v", name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, ", "))
	}

	logger.Log.Info("Virtual interface cleanup completed")
	return nil
}
