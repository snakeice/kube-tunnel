package proxy

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

// UnifiedVirtualInterfaceManager provides a cross-platform interface for virtual interface management.
type UnifiedVirtualInterfaceManager struct {
	virtualInterface VirtualInterfaceProvider
	trafficRedirect  TrafficRedirectionProvider
	routeManager     RouteProvider
	mu               sync.RWMutex
	enabled          bool
}

// NewUnifiedVirtualInterfaceManager creates a new unified virtual interface manager.
func NewUnifiedVirtualInterfaceManager() *UnifiedVirtualInterfaceManager {
	return &UnifiedVirtualInterfaceManager{
		virtualInterface: NewVirtualInterfaceProvider(),
		trafficRedirect:  NewTrafficRedirectionProvider(),
		routeManager:     NewRouteProvider(),
		enabled:          false,
	}
}

// IsSupported checks if virtual interface management is supported on the current platform.
func (uvim *UnifiedVirtualInterfaceManager) IsSupported() bool {
	return uvim.virtualInterface.IsSupported() ||
		uvim.trafficRedirect.IsSupported() ||
		uvim.routeManager.IsSupported()
}

// CheckRequirements verifies that all components can be used on the current platform.
func (uvim *UnifiedVirtualInterfaceManager) CheckRequirements() error {
	var errors []string

	if uvim.virtualInterface.IsSupported() {
		if err := uvim.virtualInterface.CheckRequirements(); err != nil {
			errors = append(errors, fmt.Sprintf("virtual interface: %v", err))
		}
	}

	if uvim.trafficRedirect.IsSupported() {
		if err := uvim.trafficRedirect.CheckRequirements(); err != nil {
			errors = append(errors, fmt.Sprintf("traffic redirection: %v", err))
		}
	}

	if uvim.routeManager.IsSupported() {
		if err := uvim.routeManager.CheckRequirements(); err != nil {
			errors = append(errors, fmt.Sprintf("route management: %v", err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("requirement check failed: %v", errors)
	}

	return nil
}

// Enable enables virtual interface management.
func (uvim *UnifiedVirtualInterfaceManager) Enable() error {
	uvim.mu.Lock()
	defer uvim.mu.Unlock()

	if uvim.enabled {
		return nil
	}

	// Check requirements before enabling
	if err := uvim.CheckRequirements(); err != nil {
		return fmt.Errorf("cannot enable virtual interface management: %w", err)
	}

	uvim.enabled = true

	logger.Log.WithField("platform", runtime.GOOS).Info("Virtual interface management enabled")
	return nil
}

// Disable disables virtual interface management and cleans up.
func (uvim *UnifiedVirtualInterfaceManager) Disable() error {
	uvim.mu.Lock()
	defer uvim.mu.Unlock()

	if !uvim.enabled {
		return nil
	}

	// Cleanup all components
	var errors []string

	if uvim.virtualInterface.IsSupported() {
		if err := uvim.virtualInterface.Cleanup(); err != nil {
			errors = append(errors, fmt.Sprintf("virtual interface cleanup: %v", err))
		}
	}

	if uvim.trafficRedirect.IsSupported() {
		if err := uvim.trafficRedirect.Cleanup(); err != nil {
			errors = append(errors, fmt.Sprintf("traffic redirection cleanup: %v", err))
		}
	}

	if uvim.routeManager.IsSupported() {
		if err := uvim.routeManager.Cleanup(); err != nil {
			errors = append(errors, fmt.Sprintf("route management cleanup: %v", err))
		}
	}

	uvim.enabled = false

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors)
	}

	logger.Log.Info("Virtual interface management disabled")
	return nil
}

// CreateVirtualInterface creates a virtual interface with the specified name and IP.
func (uvim *UnifiedVirtualInterfaceManager) CreateVirtualInterface(name, ip string) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.virtualInterface.IsSupported() {
		return fmt.Errorf("virtual interfaces are not supported on %s", runtime.GOOS)
	}

	return uvim.virtualInterface.CreateVirtualInterface(name, ip)
}

// DestroyVirtualInterface removes a virtual interface by name.
func (uvim *UnifiedVirtualInterfaceManager) DestroyVirtualInterface(name string) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.virtualInterface.IsSupported() {
		return fmt.Errorf("virtual interfaces are not supported on %s", runtime.GOOS)
	}

	return uvim.virtualInterface.DestroyVirtualInterface(name)
}

// AddRedirectionRule adds a traffic redirection rule.
func (uvim *UnifiedVirtualInterfaceManager) AddRedirectionRule(
	fromPort, toPort int,
	fromIP, toIP string,
) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.trafficRedirect.IsSupported() {
		return fmt.Errorf("traffic redirection is not supported on %s", runtime.GOOS)
	}

	return uvim.trafficRedirect.AddRedirectionRule(fromPort, toPort, fromIP, toIP)
}

// RemoveRedirectionRule removes a traffic redirection rule.
func (uvim *UnifiedVirtualInterfaceManager) RemoveRedirectionRule(
	fromPort, toPort int,
	fromIP, toIP string,
) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.trafficRedirect.IsSupported() {
		return fmt.Errorf("traffic redirection is not supported on %s", runtime.GOOS)
	}

	return uvim.trafficRedirect.RemoveRedirectionRule(fromPort, toPort, fromIP, toIP)
}

// AddRoute adds a route to the routing table.
func (uvim *UnifiedVirtualInterfaceManager) AddRoute(
	destination, gateway, interfaceName string,
	metric int,
) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.routeManager.IsSupported() {
		return fmt.Errorf("route management is not supported on %s", runtime.GOOS)
	}

	return uvim.routeManager.AddRoute(destination, gateway, interfaceName, metric)
}

// RemoveRoute removes a route from the routing table.
func (uvim *UnifiedVirtualInterfaceManager) RemoveRoute(
	destination, gateway, interfaceName string,
) error {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	if !uvim.enabled {
		return errors.New("virtual interface management is not enabled")
	}

	if !uvim.routeManager.IsSupported() {
		return fmt.Errorf("route management is not supported on %s", runtime.GOOS)
	}

	return uvim.routeManager.RemoveRoute(destination, gateway, interfaceName)
}

// GetStatus returns the status of virtual interface management.
func (uvim *UnifiedVirtualInterfaceManager) GetStatus() map[string]any {
	uvim.mu.RLock()
	defer uvim.mu.RUnlock()

	status := map[string]any{
		"enabled":                     uvim.enabled,
		"platform":                    runtime.GOOS,
		"virtual_interface_support":   uvim.virtualInterface.IsSupported(),
		"traffic_redirection_support": uvim.trafficRedirect.IsSupported(),
		"route_management_support":    uvim.routeManager.IsSupported(),
	}

	if uvim.enabled {
		uvim.addEnabledStatus(status)
	}

	return status
}

// addEnabledStatus adds status information when virtual interface management is enabled.
func (uvim *UnifiedVirtualInterfaceManager) addEnabledStatus(status map[string]any) {
	if uvim.virtualInterface.IsSupported() {
		if interfaces, err := uvim.virtualInterface.ListVirtualInterfaces(); err == nil {
			status["virtual_interfaces"] = interfaces
		}
	}

	if uvim.trafficRedirect.IsSupported() {
		if rules, err := uvim.trafficRedirect.ListRedirectionRules(); err == nil {
			status["redirection_rules"] = rules
		}
	}

	if uvim.routeManager.IsSupported() {
		if routes, err := uvim.routeManager.ListRoutes(); err == nil {
			status["routes"] = routes
		}
	}
}
