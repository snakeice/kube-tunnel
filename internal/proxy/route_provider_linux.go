//go:build linux
// +build linux

package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	osLinux = "linux"
)

// LinuxRouteManager manages routing table entries for virtual interfaces on Linux.
type LinuxRouteManager struct {
	routes map[string]*LinuxRoute
	mu     sync.RWMutex
}

// LinuxRoute represents a routing table entry.
type LinuxRoute struct {
	Destination string // Destination network (e.g., "127.0.0.2/32")
	Gateway     string // Gateway IP address
	Interface   string // Interface name (e.g., "dummy0")
	Metric      int    // Route metric/priority
	Added       bool   // Whether this route was added by us
}

// NewRouteProvider creates a new Linux route manager.
func NewRouteProvider() RouteProvider {
	return &LinuxRouteManager{
		routes: make(map[string]*LinuxRoute),
	}
}

// IsSupported checks if route management is supported on Linux.
func (rm *LinuxRouteManager) IsSupported() bool {
	if runtime.GOOS != osLinux {
		return false
	}

	// Check if ip command is available
	_, err := exec.LookPath("ip")
	return err == nil
}

// CheckRequirements verifies that route management can be used on Linux.
func (rm *LinuxRouteManager) CheckRequirements() error {
	if runtime.GOOS != "linux" {
		return errors.New("route management is only supported on Linux")
	}

	// Check if ip command is available
	_, err := exec.LookPath("ip")
	if err != nil {
		return fmt.Errorf("ip command not found: %w", err)
	}

	// Check if ip can be run (requires sudo for adding routes)
	cmd := exec.Command("ip", "route", "show")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unable to run ip route command: %w", err)
	}

	return nil
}

// AddRoute adds a route to the routing table on Linux.
func (rm *LinuxRouteManager) AddRoute(
	destination, gateway, interfaceName string,
	metric int,
) error {
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

	// Add the route using the ip command
	// Example: sudo ip route add 127.0.0.2/32 via 127.0.0.1 dev lo metric 100
	args := []string{"route", "add", destination}

	if gateway != "" {
		args = append(args, "via", gateway)
	}

	if interfaceName != "" {
		args = append(args, "dev", interfaceName)
	}

	if metric > 0 {
		args = append(args, "metric", strconv.Itoa(metric))
	}

	cmd := exec.Command("sudo", append([]string{"ip"}, args...)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add route %s -> %s: %w", destination, gateway, err)
	}

	// Store the route information
	rm.routes[key] = &LinuxRoute{
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
		"metric":      metric,
	}).Info("Added route")

	return nil
}

// RemoveRoute removes a route from the routing table on Linux.
func (rm *LinuxRouteManager) RemoveRoute(destination, gateway, interfaceName string) error {
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

	// Remove the route using the ip command
	// Example: sudo ip route del 127.0.0.2/32 via 127.0.0.1 dev lo
	args := []string{"route", "del", destination}

	if gateway != "" {
		args = append(args, "via", gateway)
	}

	if interfaceName != "" {
		args = append(args, "dev", interfaceName)
	}

	cmd := exec.Command("sudo", append([]string{"ip"}, args...)...)
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

// ListRoutes lists all routes managed by this provider.
func (rm *LinuxRouteManager) ListRoutes() ([]string, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var routes []string
	for _, route := range rm.routes {
		routeStr := fmt.Sprintf(
			"%s -> %s (via %s)",
			route.Destination,
			route.Gateway,
			route.Interface,
		)
		routes = append(routes, routeStr)
	}
	return routes, nil
}

// Cleanup removes all routes added by this manager.
func (rm *LinuxRouteManager) Cleanup() error {
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
