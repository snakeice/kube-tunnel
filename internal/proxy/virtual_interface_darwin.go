//go:build darwin
// +build darwin

package proxy

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// DarwinVirtualInterfaceManager manages virtual loopback interfaces on macOS using ifconfig
type DarwinVirtualInterfaceManager struct {
	interfaces map[string]*DarwinVirtualInterface
	mu         sync.RWMutex
}

// DarwinVirtualInterface represents a virtual loopback interface on macOS
type DarwinVirtualInterface struct {
	Name    string // Interface name (e.g., "lo1")
	IP      net.IP // Interface IP address
	Created bool   // Whether this interface was created by us
}

// NewVirtualInterfaceProvider creates a new macOS virtual interface manager
func NewVirtualInterfaceProvider() VirtualInterfaceProvider {
	return &DarwinVirtualInterfaceManager{
		interfaces: make(map[string]*DarwinVirtualInterface),
	}
}

// IsSupported checks if virtual interface creation is supported on macOS
func (vim *DarwinVirtualInterfaceManager) IsSupported() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Check if ifconfig is available
	_, err := exec.LookPath("ifconfig")
	return err == nil
}

// CheckRequirements verifies that virtual interface creation can be used on macOS
func (vim *DarwinVirtualInterfaceManager) CheckRequirements() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("virtual interface management is only supported on macOS")
	}

	// Check if ifconfig command is available
	_, err := exec.LookPath("ifconfig")
	if err != nil {
		return fmt.Errorf("ifconfig command not found: %w", err)
	}

	// Check if we can run ifconfig (requires sudo for creating interfaces)
	cmd := exec.Command("ifconfig", "-a")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run ifconfig: %w", err)
	}

	return nil
}

// CreateVirtualInterface creates a virtual loopback interface using ifconfig on macOS
func (vim *DarwinVirtualInterfaceManager) CreateVirtualInterface(name, ipStr string) error {
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

	// Find available loopback interface number if using loX format
	if strings.HasPrefix(name, "lo") && len(name) > 2 {
		actualName, err := vim.findAvailableLoopback()
		if err == nil && actualName != name {
			logger.Log.WithFields(logrus.Fields{
				"requested": name,
				"actual":    actualName,
			}).Info("Using available loopback interface")
			name = actualName
		}
	}

	// Create the interface using ifconfig
	// On macOS, we create an alias on an existing loopback interface
	cmd := exec.Command("sudo", "ifconfig", name, "alias", ipStr)
	if err := cmd.Run(); err != nil {
		// If direct creation fails, try creating on lo0
		if name != "lo0" {
			logger.Log.WithField("interface", name).Debug("Failed to create interface directly, trying lo0 alias")
			cmd = exec.Command("sudo", "ifconfig", "lo0", "alias", ipStr)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to create virtual interface %s with IP %s: %w", name, ipStr, err)
			}
			// Update name to reflect that we're using lo0
			name = "lo0"
		} else {
			return fmt.Errorf("failed to create virtual interface %s with IP %s: %w", name, ipStr, err)
		}
	}

	// Store the interface information
	vim.interfaces[name] = &DarwinVirtualInterface{
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

// DestroyVirtualInterface removes a virtual loopback interface on macOS
func (vim *DarwinVirtualInterfaceManager) DestroyVirtualInterface(name string) error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	iface, exists := vim.interfaces[name]
	if !exists {
		logger.Log.WithField("interface", name).Warn("Interface not tracked, attempting to remove anyway")
	}

	// Remove the interface alias using ifconfig
	var cmd *exec.Cmd
	if iface != nil && iface.IP != nil {
		cmd = exec.Command("sudo", "ifconfig", name, "-alias", iface.IP.String())
	} else {
		// Fallback: try to remove from lo0 if we don't have the exact IP
		cmd = exec.Command("sudo", "ifconfig", name, "down")
	}

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

// IsInterfaceExists checks if a network interface exists on macOS
func (vim *DarwinVirtualInterfaceManager) IsInterfaceExists(name string) bool {
	cmd := exec.Command("ifconfig", name)
	return cmd.Run() == nil
}

// GetInterfaceIP gets the IP address of a network interface on macOS
func (vim *DarwinVirtualInterfaceManager) GetInterfaceIP(name string) (net.IP, error) {
	cmd := exec.Command("ifconfig", name)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %s info: %w", name, err)
	}

	// Parse ifconfig output to find inet address
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				ip := net.ParseIP(parts[1])
				if ip != nil {
					return ip, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no IP address found for interface %s", name)
}

// ListVirtualInterfaces lists all virtual interfaces managed by this provider
func (vim *DarwinVirtualInterfaceManager) ListVirtualInterfaces() ([]string, error) {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	var interfaces []string
	for name := range vim.interfaces {
		interfaces = append(interfaces, name)
	}
	return interfaces, nil
}

// Cleanup removes all virtual interfaces created by this manager
func (vim *DarwinVirtualInterfaceManager) Cleanup() error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	var errors []string
	for name := range vim.interfaces {
		if err := vim.DestroyVirtualInterface(name); err != nil {
			errors = append(errors, fmt.Sprintf("failed to cleanup interface %s: %v", name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, ", "))
	}

	logger.Log.Info("Virtual interface cleanup completed")
	return nil
}

// findAvailableLoopback finds an available loopback interface (lo1, lo2, etc.)
func (vim *DarwinVirtualInterfaceManager) findAvailableLoopback() (string, error) {
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("lo%d", i)
		if !vim.IsInterfaceExists(name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no available loopback interface found")
}
