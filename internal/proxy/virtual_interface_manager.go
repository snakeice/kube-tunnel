package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	osDarwin = "darwin"
)

// VirtualInterfaceManager manages virtual network interfaces on macOS.
type VirtualInterfaceManager struct {
	interfaces map[string]*VirtualInterface
	mu         sync.RWMutex
	nextIP     int // For generating sequential IPs like 127.0.0.2, 127.0.0.3, etc.
}

// VirtualInterface represents a virtual network interface.
type VirtualInterface struct {
	Name    string
	IP      string
	Netmask string
	Port    int
	Active  bool
}

// NewVirtualInterfaceManager creates a new virtual interface manager.
func NewVirtualInterfaceManager() *VirtualInterfaceManager {
	return &VirtualInterfaceManager{
		interfaces: make(map[string]*VirtualInterface),
		nextIP:     2, // Start from 127.0.0.2 (127.0.0.1 is already used)
	}
}

// CreateVirtualInterface creates a new virtual loopback interface on macOS.
func (vim *VirtualInterfaceManager) CreateVirtualInterface(port int) (*VirtualInterface, error) {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	// Check if running on macOS
	if runtime.GOOS != osDarwin {
		return nil, errors.New("virtual interfaces are only supported on macOS")
	}

	// Find an available loopback interface name
	ifaceName, err := vim.findAvailableInterface()
	if err != nil {
		return nil, fmt.Errorf("failed to find available interface: %w", err)
	}

	// Generate IP address
	ip := fmt.Sprintf("127.0.0.%d", vim.nextIP)
	vim.nextIP++

	// Create the interface using ifconfig
	if err := vim.createLoopbackInterface(ifaceName, ip); err != nil {
		return nil, fmt.Errorf("failed to create interface %s: %w", ifaceName, err)
	}

	virtualIface := &VirtualInterface{
		Name:    ifaceName,
		IP:      ip,
		Netmask: "255.255.255.255", // /32 for individual IPs
		Port:    port,
		Active:  true,
	}

	vim.interfaces[ifaceName] = virtualIface

	logger.Log.WithFields(logrus.Fields{
		"interface": ifaceName,
		"ip":        ip,
		"port":      port,
	}).Info("🖥️  Created virtual interface on macOS")

	return virtualIface, nil
}

// DestroyVirtualInterface removes a virtual interface.
func (vim *VirtualInterfaceManager) DestroyVirtualInterface(ifaceName string) error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	virtualIface, exists := vim.interfaces[ifaceName]
	if !exists {
		return fmt.Errorf("interface %s does not exist", ifaceName)
	}

	// Remove the interface using ifconfig
	if err := vim.destroyLoopbackInterface(ifaceName); err != nil {
		return fmt.Errorf("failed to destroy interface %s: %w", ifaceName, err)
	}

	delete(vim.interfaces, ifaceName)

	logger.Log.WithFields(logrus.Fields{
		"interface": ifaceName,
		"ip":        virtualIface.IP,
	}).Info("🗑️  Destroyed virtual interface")

	return nil
}

// GetInterface returns information about a virtual interface.
func (vim *VirtualInterfaceManager) GetInterface(ifaceName string) (*VirtualInterface, bool) {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	iface, exists := vim.interfaces[ifaceName]
	return iface, exists
}

// ListInterfaces returns all managed virtual interfaces.
func (vim *VirtualInterfaceManager) ListInterfaces() map[string]*VirtualInterface {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	result := make(map[string]*VirtualInterface)
	for name, iface := range vim.interfaces {
		result[name] = &VirtualInterface{
			Name:    iface.Name,
			IP:      iface.IP,
			Netmask: iface.Netmask,
			Port:    iface.Port,
			Active:  iface.Active,
		}
	}
	return result
}

// CleanupAll removes all managed virtual interfaces.
func (vim *VirtualInterfaceManager) CleanupAll() error {
	vim.mu.Lock()
	defer vim.mu.Unlock()

	var errors []error
	for ifaceName := range vim.interfaces {
		if err := vim.destroyLoopbackInterface(ifaceName); err != nil {
			errors = append(errors, fmt.Errorf("failed to cleanup %s: %w", ifaceName, err))
		}
	}

	vim.interfaces = make(map[string]*VirtualInterface)

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors)
	}

	logger.Log.Info("🧹 Cleaned up all virtual interfaces")
	return nil
}

// IsSupported returns true if virtual interfaces are supported on the current platform.
func (vim *VirtualInterfaceManager) IsSupported() bool {
	if runtime.GOOS != osDarwin {
		return false
	}

	// Check if ifconfig is available
	cmd := exec.Command("ifconfig", "--help")
	return cmd.Run() == nil
}

// CheckRequirements checks if virtual interface creation is possible.
func (vim *VirtualInterfaceManager) CheckRequirements() error {
	if runtime.GOOS != osDarwin {
		return errors.New("virtual interfaces are only supported on macOS")
	}

	// Check if ifconfig is available
	if _, err := exec.LookPath("ifconfig"); err != nil {
		return fmt.Errorf("ifconfig command not found: %w", err)
	}

	// Test if we can read network interfaces (basic permission check)
	cmd := exec.Command("ifconfig", "-a")
	if err := cmd.Run(); err != nil {
		return errors.New("insufficient privileges to access network interfaces")
	}

	return nil
}

// findAvailableInterface finds an available loopback interface name.
func (vim *VirtualInterfaceManager) findAvailableInterface() (string, error) {
	// Try lo1, lo2, lo3, etc.
	for i := 1; i <= 255; i++ {
		ifaceName := fmt.Sprintf("lo%d", i)

		// Check if interface already exists in system
		if !vim.interfaceExists(ifaceName) {
			return ifaceName, nil
		}
	}

	return "", errors.New("no available loopback interfaces found")
}

// interfaceExists checks if a network interface exists in the system.
func (vim *VirtualInterfaceManager) interfaceExists(ifaceName string) bool {
	cmd := exec.Command("ifconfig", ifaceName)
	return cmd.Run() == nil
}

// createLoopbackInterface creates a loopback interface using ifconfig.
func (vim *VirtualInterfaceManager) createLoopbackInterface(ifaceName, ip string) error {
	// Create the loopback interface
	// On macOS: sudo ifconfig lo1 create
	cmd := exec.Command("sudo", "ifconfig", ifaceName, "create")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create interface: %w", err)
	}

	// Assign IP address
	// On macOS: sudo ifconfig lo1 inet 127.0.0.2 netmask 255.255.255.255
	cmd = exec.Command("sudo", "ifconfig", ifaceName, "inet", ip, "netmask", "255.255.255.255")
	if err := cmd.Run(); err != nil {
		// Try to cleanup the interface if IP assignment failed
		if err := exec.Command("sudo", "ifconfig", ifaceName, "destroy").Run(); err != nil {
			logger.Log.WithError(err).
				WithField("interface", ifaceName).
				Warn("Failed to cleanup loopback interface after IP assignment failure")
		}
		return fmt.Errorf("failed to assign IP address: %w", err)
	}

	// Bring the interface up
	cmd = exec.Command("sudo", "ifconfig", ifaceName, "up")
	if err := cmd.Run(); err != nil {
		// Try to cleanup the interface if bringing it up failed
		if err := exec.Command("sudo", "ifconfig", ifaceName, "destroy").Run(); err != nil {
			logger.Log.WithError(err).
				WithField("interface", ifaceName).
				Warn("Failed to cleanup loopback interface after bringing it up failed")
		}
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	return nil
}

// destroyLoopbackInterface removes a loopback interface using ifconfig.
func (vim *VirtualInterfaceManager) destroyLoopbackInterface(ifaceName string) error {
	// On macOS: sudo ifconfig lo1 destroy
	cmd := exec.Command("sudo", "ifconfig", ifaceName, "destroy")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to destroy interface: %w", err)
	}

	return nil
}

// GetInterfaceIP returns the IP address for a given interface name.
func (vim *VirtualInterfaceManager) GetInterfaceIP(ifaceName string) (string, error) {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	iface, exists := vim.interfaces[ifaceName]
	if !exists {
		return "", fmt.Errorf("interface %s not found", ifaceName)
	}

	return iface.IP, nil
}

// ValidateInterface checks if a virtual interface is properly configured.
func (vim *VirtualInterfaceManager) ValidateInterface(ifaceName string) error {
	iface, exists := vim.GetInterface(ifaceName)
	if !exists {
		return fmt.Errorf("interface %s does not exist", ifaceName)
	}

	// Check if the interface actually exists in the system
	if !vim.interfaceExists(ifaceName) {
		return fmt.Errorf("interface %s exists in manager but not in system", ifaceName)
	}

	// Check if the IP is reachable
	if err := vim.pingInterface(iface.IP); err != nil {
		return fmt.Errorf("interface %s IP %s is not reachable: %w", ifaceName, iface.IP, err)
	}

	return nil
}

// pingInterface tests if an interface IP is reachable.
func (vim *VirtualInterfaceManager) pingInterface(ip string) error {
	cmd := exec.Command("ping", "-c", "1", "-W", "1000", ip)
	return cmd.Run()
}

// GetStatistics returns statistics for virtual interfaces.
func (vim *VirtualInterfaceManager) GetStatistics() map[string]any {
	vim.mu.RLock()
	defer vim.mu.RUnlock()

	stats := map[string]any{
		"total_interfaces": len(vim.interfaces),
		"platform":         runtime.GOOS,
		"supported":        vim.IsSupported(),
		"next_ip":          fmt.Sprintf("127.0.0.%d", vim.nextIP),
	}

	interfaces := make([]map[string]any, 0, len(vim.interfaces))

	for _, iface := range vim.interfaces {
		ifaceStats := map[string]any{
			"name":    iface.Name,
			"ip":      iface.IP,
			"netmask": iface.Netmask,
			"port":    iface.Port,
			"active":  iface.Active,
		}

		interfaces = append(interfaces, ifaceStats)
	}

	stats["interfaces"] = interfaces

	return stats
}
