package proxy

import "net"

// VirtualInterfaceProvider defines the interface for virtual interface management across platforms.
type VirtualInterfaceProvider interface {
	// CreateVirtualInterface creates a virtual interface with the specified name and IP
	CreateVirtualInterface(name, ip string) error

	// DestroyVirtualInterface removes a virtual interface by name
	DestroyVirtualInterface(name string) error

	// IsInterfaceExists checks if an interface exists
	IsInterfaceExists(name string) bool

	// GetInterfaceIP gets the IP address of an interface
	GetInterfaceIP(name string) (net.IP, error)

	// IsSupported returns true if virtual interfaces are supported on this platform
	IsSupported() bool

	// CheckRequirements verifies that all requirements are met for virtual interface creation
	CheckRequirements() error

	// ListVirtualInterfaces lists all virtual interfaces managed by this provider
	ListVirtualInterfaces() ([]string, error)

	// Cleanup performs any necessary cleanup when shutting down
	Cleanup() error
}

// TrafficRedirectionProvider defines the interface for traffic redirection across platforms.
type TrafficRedirectionProvider interface {
	// AddRedirectionRule adds a traffic redirection rule
	AddRedirectionRule(fromPort, toPort int, fromIP, toIP string) error

	// RemoveRedirectionRule removes a traffic redirection rule
	RemoveRedirectionRule(fromPort, toPort int, fromIP, toIP string) error

	// IsSupported returns true if traffic redirection is supported on this platform
	IsSupported() bool

	// CheckRequirements verifies that all requirements are met for traffic redirection
	CheckRequirements() error

	// ListRedirectionRules lists all active redirection rules
	ListRedirectionRules() ([]string, error)

	// Cleanup removes all redirection rules added by this provider
	Cleanup() error
}

// RouteProvider defines the interface for route management across platforms.
type RouteProvider interface {
	// AddRoute adds a route to the routing table
	AddRoute(destination, gateway, interfaceName string, metric int) error

	// RemoveRoute removes a route from the routing table
	RemoveRoute(destination, gateway, interfaceName string) error

	// IsSupported returns true if route management is supported on this platform
	IsSupported() bool

	// CheckRequirements verifies that all requirements are met for route management
	CheckRequirements() error

	// ListRoutes lists all routes managed by this provider
	ListRoutes() ([]string, error)

	// Cleanup removes all routes added by this provider
	Cleanup() error
}
