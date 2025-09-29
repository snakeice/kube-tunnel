//go:build darwin
// +build darwin

package proxy

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// DarwinRouteManager manages routing table entries for virtual interfaces on macOS
type DarwinRouteManager struct {
	routes map[string]*DarwinRoute
	mu     sync.RWMutex
}

// DarwinRoute represents a routing table entry
type DarwinRoute struct {
	Destination string // Destination network (e.g., "127.0.0.2/32")
	Gateway     string // Gateway IP address
	Interface   string // Interface name (e.g., "lo1")
	Metric      int    // Route metric/priority
	Added       bool   // Whether this route was added by us
}

// NewRouteProvider creates a new macOS route manager
func NewRouteProvider() RouteProvider {
	return &DarwinRouteManager{
		routes: make(map[string]*DarwinRoute),
	}
}

// IsSupported checks if route management is supported on macOS
func (rm *DarwinRouteManager) IsSupported() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Check if route command is available
	_, err := exec.LookPath("route")
	return err == nil
}

// CheckRequirements verifies that route management can be used on macOS
func (rm *DarwinRouteManager) CheckRequirements() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("route management is only supported on macOS")
	}

	// Check if route command is available
	_, err := exec.LookPath("route")
	if err != nil {
		return fmt.Errorf("route command not found: %w", err)
	}

	// Check if route can be run (requires sudo for adding routes)
	cmd := exec.Command("route", "-n", "get", "default")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run route command: %w", err)
	}

	return nil
}

// AddRoute adds a route to the routing table on macOS
func (rm *DarwinRouteManager) AddRoute(destination, gateway, interfaceName string, metric int) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Create unique key for this route
	key := fmt.Sprintf("%s->%s", destination, gateway)

	// Check if route already exists
	if _, exists := rm.routes[key]; exists {
		logger.Log.WithFields(logrus.Fields{
			"destination": destination,
			"gateway":     gateway,
			"interface":   interfaceName,
		}).Warn("Route already exists")
		return nil
	}

	// Add the route using the route command
	// Example: sudo route add -host 127.0.0.2 127.0.0.1
	var cmd *exec.Cmd
	if strings.Contains(destination, "/") {
		// Network route
		parts := strings.Split(destination, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid network destination format: %s", destination)
		}
		cmd = exec.Command("sudo", "route", "add", "-net", parts[0], gateway)
	} else {
		// Host route
		cmd = exec.Command("sudo", "route", "add", "-host", destination, gateway)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add route %s -> %s: %w", destination, gateway, err)
	}

	// Store the route information
	rm.routes[key] = &DarwinRoute{
		Destination: destination,
		Gateway:     gateway,
		Interface:   interfaceName,
		Metric:      metric,
		Added:       true,
	}

	logger.Log.WithFields(logrus.Fields{
		"destination": destination,
		"gateway":     gateway,
		"interface":   interfaceName,
	}).Info("Added route")

	return nil
}

// RemoveRoute removes a route from the routing table on macOS
func (rm *DarwinRouteManager) RemoveRoute(destination, gateway, interfaceName string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	key := fmt.Sprintf("%s->%s", destination, gateway)
	route, exists := rm.routes[key]
	if !exists {
		logger.Log.WithFields(logrus.Fields{
			"destination": destination,
			"gateway":     gateway,
		}).Warn("Route not found for removal")
		return nil
	}

	// Remove the route using the route command
	var cmd *exec.Cmd
	if strings.Contains(destination, "/") {
		// Network route
		parts := strings.Split(destination, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid network destination format: %s", destination)
		}
		cmd = exec.Command("sudo", "route", "delete", "-net", parts[0], gateway)
	} else {
		// Host route
		cmd = exec.Command("sudo", "route", "delete", "-host", destination, gateway)
	}

	if err := cmd.Run(); err != nil {
		logger.Log.WithFields(logrus.Fields{
			"destination": destination,
			"gateway":     gateway,
			"error":       err,
		}).Warn("Failed to remove route")
		return fmt.Errorf("failed to remove route %s -> %s: %w", destination, gateway, err)
	}

	delete(rm.routes, key)

	logger.Log.WithFields(logrus.Fields{
		"destination": destination,
		"gateway":     gateway,
		"interface":   route.Interface,
	}).Info("Removed route")

	return nil
}

// ListRoutes lists all routes managed by this provider
func (rm *DarwinRouteManager) ListRoutes() ([]string, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var routes []string
	for _, route := range rm.routes {
		routeStr := fmt.Sprintf("%s -> %s (via %s)", route.Destination, route.Gateway, route.Interface)
		routes = append(routes, routeStr)
	}
	return routes, nil
}

// Cleanup removes all routes added by this manager
func (rm *DarwinRouteManager) Cleanup() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var errors []string
	for key, route := range rm.routes {
		if err := rm.RemoveRoute(route.Destination, route.Gateway, route.Interface); err != nil {
			errors = append(errors, fmt.Sprintf("failed to cleanup route %s: %v", key, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, ", "))
	}

	logger.Log.Info("Route cleanup completed")
	return nil
}
