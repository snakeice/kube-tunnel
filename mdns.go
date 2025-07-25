package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
	"github.com/sirupsen/logrus"
)

const (
	proxyAddress   = "127.0.0.1" // Where our proxy is running
	proxyPort      = 80
	serviceTTL     = 60 // TTL in seconds
	browseTimeout  = 3 * time.Second
	resolveTimeout = 2 * time.Second
)

type ZeroconfServer struct {
	server   *zeroconf.Server
	running  bool
	shutdown chan bool
	services map[string]*ServiceInfo
	mutex    sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

type ServiceInfo struct {
	Service   string
	Namespace string
	Port      int
	LastSeen  time.Time
}

// NewZeroconfServer creates a new zeroconf-based mDNS server instance.
func NewZeroconfServer() *ZeroconfServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &ZeroconfServer{
		shutdown: make(chan bool, 1),
		services: make(map[string]*ServiceInfo),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start begins the zeroconf server for Kubernetes service discovery.
func (z *ZeroconfServer) Start() {
	z.running = true

	log.WithFields(logrus.Fields{
		"proxy_address": proxyAddress,
		"proxy_port":    proxyPort,
		"ttl":           serviceTTL,
	}).Info("🌐 Starting zeroconf server for *.svc.cluster.local")

	// Register our proxy as a general Kubernetes service gateway
	if err := z.registerProxyService(); err != nil {
		log.WithField("error", err.Error()).
			Warn("⚠️  Failed to register proxy service (continuing anyway)")
	}

	// Start background service discovery and monitoring with proper error handling
	go z.safeMonitorServices()
	go z.monitorShutdown()

	log.Info("✅ Zeroconf server started successfully")
}

// Stop gracefully shuts down the zeroconf server.
func (z *ZeroconfServer) Stop() {
	if !z.running {
		return
	}

	log.Info("🛑 Stopping zeroconf server")
	z.running = false

	// Cancel context first to stop all operations
	z.cancel()

	// Then close shutdown channel
	select {
	case z.shutdown <- true:
	default:
		// Channel already has a value or is closed
	}

	// Shutdown registered services
	if z.server != nil {
		z.server.Shutdown()
		log.Debug("🔌 Zeroconf service registration shutdown")
	}

	log.Info("✅ Zeroconf server stopped")
}

// registerProxyService registers our proxy as a service discovery gateway.
func (z *ZeroconfServer) registerProxyService() error {
	hostname, err := getHostname()
	if err != nil {
		hostname = "kube-tunnel"
	}

	// Register as a generic gateway service
	serviceName := "_kube-tunnel._tcp"
	instanceName := hostname + "-proxy"

	text := []string{
		"version=1.0",
		"purpose=kubernetes-service-proxy",
		"domains=*.svc.cluster.local",
		fmt.Sprintf("proxy=%s:%d", proxyAddress, proxyPort),
	}

	server, err := zeroconf.Register(
		instanceName, // instance name
		serviceName,  // service type
		"local.",     // domain
		proxyPort,    // port
		text,         // TXT records
		nil,          // interfaces (nil = all)
	)

	if err != nil {
		return fmt.Errorf("failed to register proxy service: %w", err)
	}

	z.server = server

	log.WithFields(logrus.Fields{
		"instance": instanceName,
		"service":  serviceName,
		"port":     proxyPort,
		"txt":      text,
	}).Info("📡 Registered proxy service via zeroconf")

	return nil
}

// safeMonitorServices wraps service monitoring with panic recovery.
func (z *ZeroconfServer) safeMonitorServices() {
	defer func() {
		if r := recover(); r != nil {
			log.WithField("panic", r).Error("❌ Service monitoring panic recovered")
			// Try to restart monitoring after a delay if still running
			if z.running {
				time.Sleep(5 * time.Second)
				go z.safeMonitorServices()
			}
		}
	}()

	z.monitorServices()
}

// monitorServices continuously discovers and tracks Kubernetes services.
func (z *ZeroconfServer) monitorServices() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Do initial discovery with delay to avoid startup issues
	time.Sleep(2 * time.Second)
	z.safeDiscoverServices()

	for {
		select {
		case <-z.ctx.Done():
			log.Debug("🌐 Service monitoring stopped by context")
			return
		case <-z.shutdown:
			log.Debug("🌐 Service monitoring stopped by shutdown signal")
			return
		case <-ticker.C:
			z.safeDiscoverServices()
			z.cleanupStaleServices()
		}
	}
}

// safeDiscoverServices wraps service discovery with panic recovery.
func (z *ZeroconfServer) safeDiscoverServices() {
	defer func() {
		if r := recover(); r != nil {
			log.WithField("panic", r).Warn("⚠️  Service discovery panic recovered")
		}
	}()

	z.discoverServices()
}

// discoverServices performs service discovery for Kubernetes services.
func (z *ZeroconfServer) discoverServices() {
	// Common Kubernetes service types to discover
	serviceTypes := []string{
		"_http._tcp",
		"_https._tcp",
		"_grpc._tcp",
	}

	for _, serviceType := range serviceTypes {
		if !z.running {
			return
		}
		// Add delay between service type queries to avoid overwhelming the network
		time.Sleep(100 * time.Millisecond)
		z.safeBrowseServiceType(serviceType)
	}
}

// safeBrowseServiceType wraps browseServiceType with panic recovery.
func (z *ZeroconfServer) safeBrowseServiceType(serviceType string) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(logrus.Fields{
				"service_type": serviceType,
				"panic":        r,
			}).Warn("⚠️  Service type browsing panic recovered")
		}
	}()

	z.browseServiceType(serviceType)
}

// browseServiceType discovers services of a specific type with proper error handling.
func (z *ZeroconfServer) browseServiceType(serviceType string) {
	// Create a context with timeout for this browse operation
	ctx, cancel := context.WithTimeout(z.ctx, browseTimeout)
	defer cancel()

	// Create entries channel
	entries := make(chan *zeroconf.ServiceEntry, 10) // Buffered to prevent blocking

	// Start a goroutine to handle entries
	done := make(chan bool, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.WithFields(logrus.Fields{
					"service_type": serviceType,
					"panic":        r,
				}).Warn("⚠️  Service entry processing panic recovered")
			}
			select {
			case done <- true:
			default:
			}
		}()

		for {
			select {
			case entry, ok := <-entries:
				if !ok {
					return // Channel closed
				}
				z.processServiceEntry(entry)
			case <-ctx.Done():
				return // Context cancelled
			}
		}
	}()

	// Perform the browse operation with error handling
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.WithFields(logrus.Fields{
					"service_type": serviceType,
					"panic":        r,
				}).Warn("⚠️  Browse operation panic recovered")
			}
		}()

		err := zeroconf.Browse(ctx, serviceType, "local.", entries)
		if err != nil {
			log.WithFields(logrus.Fields{
				"service_type": serviceType,
				"error":        err.Error(),
			}).Debug("🔍 Browse failed for service type")
		}
	}()

	// Wait for processing to complete or timeout
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		log.WithField("service_type", serviceType).Debug("🕐 Browse processing timeout")
	}
}

// processServiceEntry processes a discovered service entry.
func (z *ZeroconfServer) processServiceEntry(entry *zeroconf.ServiceEntry) {
	if entry == nil {
		return
	}

	// Only process Kubernetes-related services
	if !z.isKubernetesService(entry) {
		return
	}

	service, namespace := z.extractServiceInfo(entry)
	if service == "" || namespace == "" {
		return
	}

	z.mutex.Lock()
	defer z.mutex.Unlock()

	domain := fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
	z.services[domain] = &ServiceInfo{
		Service:   service,
		Namespace: namespace,
		Port:      entry.Port,
		LastSeen:  time.Now(),
	}

	log.WithFields(logrus.Fields{
		"domain":    domain,
		"service":   service,
		"namespace": namespace,
		"port":      entry.Port,
		"hostname":  entry.HostName,
		"ips":       formatIPs(entry.AddrIPv4, entry.AddrIPv6),
	}).Debug("🔍 Discovered Kubernetes service")
}

// isKubernetesService checks if a service entry is Kubernetes-related.
func (z *ZeroconfServer) isKubernetesService(entry *zeroconf.ServiceEntry) bool {
	// Check TXT records for Kubernetes indicators
	for _, txt := range entry.Text {
		txtLower := strings.ToLower(txt)
		if strings.Contains(txtLower, "kubernetes") ||
			strings.Contains(txtLower, "k8s") ||
			strings.Contains(txtLower, "namespace") {
			return true
		}
	}

	// Check instance name patterns
	instanceLower := strings.ToLower(entry.Instance)
	return strings.Contains(instanceLower, "k8s-") ||
		strings.Contains(instanceLower, "kube-") ||
		strings.Contains(instanceLower, "kubernetes")
}

// extractServiceInfo extracts service and namespace from service entry.
func (z *ZeroconfServer) extractServiceInfo(
	entry *zeroconf.ServiceEntry,
) (string, string) {
	var service, namespace string
	// Try to extract from TXT records first
	for _, txt := range entry.Text {
		if after, ok := strings.CutPrefix(txt, "service="); ok {
			service = after
		}
		if after, ok := strings.CutPrefix(txt, "namespace="); ok {
			namespace = after
		}
	}

	// If not found in TXT, try to parse from instance name
	if service == "" || namespace == "" {
		parts := strings.Split(entry.Instance, "-")
		if len(parts) >= 2 {
			// Common patterns: servicename-namespace, k8s-servicename-namespace
			if len(parts) >= 3 && parts[0] == "k8s" {
				service = parts[1]
				namespace = parts[2]
			} else {
				service = parts[0]
				namespace = parts[1]
			}
		}
	}

	// Default namespace if not specified
	if namespace == "" {
		namespace = "default"
	}

	return service, namespace
}

// cleanupStaleServices removes services that haven't been seen recently.
func (z *ZeroconfServer) cleanupStaleServices() {
	z.mutex.Lock()
	defer z.mutex.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	removed := 0

	for domain, info := range z.services {
		if info.LastSeen.Before(cutoff) {
			delete(z.services, domain)
			removed++
		}
	}

	if removed > 0 {
		log.WithFields(logrus.Fields{
			"removed": removed,
			"total":   len(z.services),
		}).Debug("🧹 Cleaned up stale services")
	}
}

// monitorShutdown watches for shutdown signal.
func (z *ZeroconfServer) monitorShutdown() {
	select {
	case <-z.shutdown:
		log.Debug("🌐 Zeroconf server shutdown signal received")
	case <-z.ctx.Done():
		log.Debug("🌐 Zeroconf server context cancelled")
	}
}

// LookupService attempts to resolve a Kubernetes service domain.
func (z *ZeroconfServer) LookupService(domain string) (*ServiceInfo, bool) {
	if !ValidateServiceDomain(domain) {
		return nil, false
	}

	z.mutex.RLock()
	defer z.mutex.RUnlock()

	info, exists := z.services[domain]
	if exists {
		// Update last seen (make a copy to avoid race conditions)
		infoCopy := &ServiceInfo{
			Service:   info.Service,
			Namespace: info.Namespace,
			Port:      info.Port,
			LastSeen:  time.Now(),
		}

		// Update the original
		z.mutex.RUnlock()
		z.mutex.Lock()
		if stillExists := z.services[domain]; stillExists != nil {
			stillExists.LastSeen = time.Now()
		}
		z.mutex.Unlock()
		z.mutex.RLock()

		log.WithFields(logrus.Fields{
			"domain":    domain,
			"service":   info.Service,
			"namespace": info.Namespace,
			"port":      info.Port,
		}).Debug("✅ Service found in cache")

		return infoCopy, true
	}

	return nil, false
}

// RegisterService registers a new Kubernetes service.
func (z *ZeroconfServer) RegisterService(service, namespace string, port int) error {
	instanceName := fmt.Sprintf("k8s-%s-%s", service, namespace)
	serviceName := "_kubernetes._tcp"

	text := []string{
		"service=" + service,
		"namespace=" + namespace,
		"type=kubernetes-service",
		"proxy=kube-tunnel",
	}

	server, err := zeroconf.Register(
		instanceName,
		serviceName,
		"local.",
		port,
		text,
		nil,
	)

	if err != nil {
		return fmt.Errorf("failed to register service %s.%s: %w", service, namespace, err)
	}

	// Store in our cache as well
	domain := fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
	z.mutex.Lock()
	z.services[domain] = &ServiceInfo{
		Service:   service,
		Namespace: namespace,
		Port:      port,
		LastSeen:  time.Now(),
	}
	z.mutex.Unlock()

	log.WithFields(logrus.Fields{
		"domain":    domain,
		"service":   service,
		"namespace": namespace,
		"port":      port,
		"instance":  instanceName,
	}).Info("📡 Registered Kubernetes service")

	// Clean shutdown of individual service registration
	go func() {
		select {
		case <-z.shutdown:
		case <-z.ctx.Done():
		}
		server.Shutdown()
	}()

	return nil
}

// GetAllServices returns a copy of all discovered services.
func (z *ZeroconfServer) GetAllServices() map[string]*ServiceInfo {
	z.mutex.RLock()
	defer z.mutex.RUnlock()

	services := make(map[string]*ServiceInfo)
	for domain, info := range z.services {
		// Create a copy
		services[domain] = &ServiceInfo{
			Service:   info.Service,
			Namespace: info.Namespace,
			Port:      info.Port,
			LastSeen:  info.LastSeen,
		}
	}

	return services
}

// SafeStart wraps the Start method with additional error recovery.
func (z *ZeroconfServer) SafeStart() {
	defer func() {
		if r := recover(); r != nil {
			log.WithField("panic", r).Error("❌ Zeroconf server start panic recovered")
		}
	}()

	z.Start()
}

// LogZeroconfStats logs statistics about zeroconf operations.
func (z *ZeroconfServer) LogZeroconfStats() {
	z.mutex.RLock()
	serviceCount := len(z.services)
	z.mutex.RUnlock()

	namespaces := make(map[string]int)
	z.mutex.RLock()
	for _, info := range z.services {
		namespaces[info.Namespace]++
	}
	z.mutex.RUnlock()

	log.WithFields(logrus.Fields{
		"total_services": serviceCount,
		"namespaces":     len(namespaces),
		"running":        z.running,
	}).Info("📊 Zeroconf service discovery statistics")

	if serviceCount > 0 {
		log.WithField("namespace_breakdown", namespaces).Debug("📊 Services by namespace")
	}
}

// Helper functions

func formatIPs(ipv4, ipv6 []net.IP) []string {
	var ips []string
	for _, ip := range ipv4 {
		ips = append(ips, ip.String())
	}
	for _, ip := range ipv6 {
		ips = append(ips, ip.String())
	}
	return ips
}

func getHostname() (string, error) {
	hostname, err := net.LookupAddr("127.0.0.1")
	if err != nil || len(hostname) == 0 {
		return "", err
	}
	return strings.TrimSuffix(hostname[0], "."), nil
}

// (keeping the existing function for compatibility).
func ValidateServiceDomain(domain string) bool {
	if !strings.HasSuffix(domain, ".svc.cluster.local") {
		return false
	}

	servicePart := strings.TrimSuffix(domain, ".svc.cluster.local")
	parts := strings.Split(servicePart, ".")

	if len(parts) < 2 {
		return false
	}

	for _, part := range parts {
		if len(part) == 0 || strings.Contains(part, "_") {
			return false
		}
	}

	return true
}

// (keeping the existing function for compatibility).
func GetServiceInfo(domain string) (string, string, string) {
	if !ValidateServiceDomain(domain) {
		return "", "", ""
	}

	servicePart := strings.TrimSuffix(domain, ".svc.cluster.local")
	parts := strings.Split(servicePart, ".")

	if len(parts) >= 2 {
		return parts[0], parts[1], "80"
	}

	return "", "", ""
}
