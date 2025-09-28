package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// RouteManager manages routing table entries for virtual interfaces on macOS.
type RouteManager struct {
	routes map[string]*Route
	mu     sync.RWMutex
}

// Route represents a routing table entry.
type Route struct {
	Destination string // Destination network (e.g., "127.0.0.2/32")
	Gateway     string // Gateway IP address
	Interface   string // Interface name (e.g., "lo1")
	Metric      int    // Route metric/priority
	Added       bool   // Whether this route was added by us
}

// NewRouteManager creates a new route manager.
func NewRouteManager() *RouteManager {
	return &RouteManager{
		routes: make(map[string]*Route),
	}
}

// IsSupported checks if route management is supported on the current platform.
func (rm *RouteManager) IsSupported() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Check if route command is available
	_, err := exec.LookPath("route")
	return err == nil
}

// CheckRequirements verifies that route management can be used.
func (rm *RouteManager) CheckRequirements() error {
	if runtime.GOOS != "darwin" {
		return errors.New("route management is only supported on macOS")
	}

	// Check if route command is available
	if _, err := exec.LookPath("route"); err != nil {
		return fmt.Errorf("route command not found: %w", err)
	}

	// Test basic route functionality
	cmd := exec.Command("route", "-n", "get", "default")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("route command requires privileges: %w", err)
	}

	return nil
}

// AddRoute adds a route to the routing table.
func (rm *RouteManager) AddRoute(destination, gateway, iface string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if !rm.IsSupported() {
		return errors.New("route management is not supported on this system")
	}

	routeKey := fmt.Sprintf("%s-%s-%s", destination, gateway, iface)

	// Check if route already exists
	if _, exists := rm.routes[routeKey]; exists {
		return fmt.Errorf("route already exists: %s via %s dev %s", destination, gateway, iface)
	}

	// Add the route using the route command
	if err := rm.addSystemRoute(destination, gateway); err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}

	// Store route information
	rm.routes[routeKey] = &Route{
		Destination: destination,
		Gateway:     gateway,
		Interface:   iface,
		Metric:      1,
		Added:       true,
	}

	logger.Log.WithFields(logrus.Fields{
		"destination": destination,
		"gateway":     gateway,
		"interface":   iface,
	}).Info("🛣️  Added route")

	return nil
}

// RemoveRoute removes a route from the routing table.
func (rm *RouteManager) RemoveRoute(destination, gateway, iface string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	routeKey := fmt.Sprintf("%s-%s-%s", destination, gateway, iface)

	route, exists := rm.routes[routeKey]
	if !exists {
		return fmt.Errorf("route not found: %s via %s dev %s", destination, gateway, iface)
	}

	// Remove the route from system
	if err := rm.deleteSystemRoute(destination); err != nil {
		return fmt.Errorf("failed to remove route: %w", err)
	}

	// Remove from our tracking
	delete(rm.routes, routeKey)

	logger.Log.WithFields(logrus.Fields{
		"destination": route.Destination,
		"gateway":     route.Gateway,
		"interface":   route.Interface,
	}).Info("🗑️  Removed route")

	return nil
}

// AddHostRoute adds a host route for a specific IP.
func (rm *RouteManager) AddHostRoute(hostIP, gateway, iface string) error {
	// Host routes use /32 netmask (single IP)
	destination := fmt.Sprintf("%s/32", hostIP)
	return rm.AddRoute(destination, gateway, iface)
}

// RemoveHostRoute removes a host route for a specific IP.
func (rm *RouteManager) RemoveHostRoute(hostIP, gateway, iface string) error {
	destination := fmt.Sprintf("%s/32", hostIP)
	return rm.RemoveRoute(destination, gateway, iface)
}

// AddInterfaceRoute adds a route that directs traffic to a specific interface.
func (rm *RouteManager) AddInterfaceRoute(network, iface string) error {
	// For interface routes on macOS, we can use the interface IP as gateway
	interfaceIP, err := rm.getInterfaceIP(iface)
	if err != nil {
		return fmt.Errorf("failed to get interface IP for %s: %w", iface, err)
	}

	return rm.AddRoute(network, interfaceIP, iface)
}

// FlushRoutes removes all routes managed by this instance.
func (rm *RouteManager) FlushRoutes() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var errors []error
	for routeKey, route := range rm.routes {
		if route.Added {
			if err := rm.deleteSystemRoute(route.Destination); err != nil {
				errors = append(errors, fmt.Errorf("failed to remove route %s: %w", routeKey, err))
			}
		}
	}

	rm.routes = make(map[string]*Route)

	if len(errors) > 0 {
		return fmt.Errorf("errors while flushing routes: %v", errors)
	}

	logger.Log.Info("🧹 Flushed all routes")
	return nil
}

// ListRoutes returns all managed routes.
func (rm *RouteManager) ListRoutes() map[string]*Route {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make(map[string]*Route)
	for key, route := range rm.routes {
		result[key] = &Route{
			Destination: route.Destination,
			Gateway:     route.Gateway,
			Interface:   route.Interface,
			Metric:      route.Metric,
			Added:       route.Added,
		}
	}
	return result
}

// GetRouteTable returns the system routing table (for debugging).
func (rm *RouteManager) GetRouteTable() (string, error) {
	if !rm.IsSupported() {
		return "", errors.New("route management not supported")
	}

	cmd := exec.Command("netstat", "-rn")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get route table: %w", err)
	}

	return string(output), nil
}

// ValidateRoute checks if a route exists in the system.
func (rm *RouteManager) ValidateRoute(destination, gateway string) error {
	if !rm.IsSupported() {
		return errors.New("route management not supported")
	}

	// Use route get to check if route exists
	cmd := exec.Command("route", "-n", "get", destination)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("route does not exist or is unreachable")
	}

	// Check if the output contains our gateway
	if !strings.Contains(string(output), gateway) {
		return errors.New("route exists but uses different gateway")
	}

	return nil
}

// GetStatistics returns route management statistics.
func (rm *RouteManager) GetStatistics() map[string]any {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	stats := map[string]any{
		"supported":      rm.IsSupported(),
		"platform":       runtime.GOOS,
		"managed_routes": len(rm.routes),
	}

	routes := make([]map[string]any, 0, len(rm.routes))
	for _, route := range rm.routes {
		routeInfo := map[string]any{
			"destination": route.Destination,
			"gateway":     route.Gateway,
			"interface":   route.Interface,
			"metric":      route.Metric,
			"added":       route.Added,
		}

		routes = append(routes, routeInfo)
	}
	stats["routes"] = routes

	// Try to get system route count
	if rm.IsSupported() {
		if output, err := rm.GetRouteTable(); err == nil {
			lines := strings.Split(output, "\n")
			stats["system_routes"] = len(lines) - 1 // Subtract header line
		}
	}

	return stats
}

// addSystemRoute adds a route to the system routing table.
func (rm *RouteManager) addSystemRoute(destination, gateway string) error {
	// Parse destination to handle both IP and IP/netmask formats
	dest := destination
	if strings.Contains(destination, "/") {
		// For network routes: sudo route -n add -net 192.168.1.0/24 gateway
		parts := strings.Split(destination, "/")
		if len(parts) == 2 && parts[1] == "32" {
			// Host route: sudo route -n add -host 192.168.1.1 gateway
			dest = parts[0]
			cmd := exec.Command("sudo", "route", "-n", "add", "-host", dest, gateway)
			return cmd.Run()
		} else {
			// Network route: sudo route -n add -net 192.168.1.0/24 gateway
			cmd := exec.Command("sudo", "route", "-n", "add", "-net", destination, gateway)
			return cmd.Run()
		}
	} else {
		// Simple host route: sudo route -n add host gateway
		cmd := exec.Command("sudo", "route", "-n", "add", dest, gateway)
		return cmd.Run()
	}
}

// deleteSystemRoute removes a route from the system routing table.
func (rm *RouteManager) deleteSystemRoute(destination string) error {
	// Parse destination for proper deletion
	dest := destination
	if strings.Contains(destination, "/") {
		parts := strings.Split(destination, "/")
		if len(parts) == 2 && parts[1] == "32" {
			// Delete host route: sudo route -n delete -host IP
			dest = parts[0]
			cmd := exec.Command("sudo", "route", "-n", "delete", "-host", dest)
			return cmd.Run()
		} else {
			// Delete network route: sudo route -n delete -net network/mask
			cmd := exec.Command("sudo", "route", "-n", "delete", "-net", destination)
			return cmd.Run()
		}
	} else {
		// Delete simple route: sudo route -n delete dest
		cmd := exec.Command("sudo", "route", "-n", "delete", dest)
		return cmd.Run()
	}
}

// getInterfaceIP gets the IP address of a network interface.
func (rm *RouteManager) getInterfaceIP(iface string) (string, error) {
	cmd := exec.Command("ifconfig", iface)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get interface info: %w", err)
	}

	// Parse ifconfig output to extract IP address
	lines := strings.SplitSeq(string(output), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1], nil
			}
		}
	}

	return "", fmt.Errorf("no IP address found for interface %s", iface)
}

// TestRoute tests if a route is working by checking connectivity.
func (rm *RouteManager) TestRoute(destination string) error {
	if !rm.IsSupported() {
		return errors.New("route testing not supported")
	}

	// Use ping to test route connectivity
	dest := destination
	if strings.Contains(destination, "/") {
		// Extract IP from network notation
		dest = strings.Split(destination, "/")[0]
	}

	cmd := exec.Command("ping", "-c", "1", "-W", "1000", dest)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("route to %s is not working: %w", dest, err)
	}

	return nil
}
