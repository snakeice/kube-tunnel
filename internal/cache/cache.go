package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/k8s"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

const (
	idleTimeout = 5 * time.Minute
	localhostIP = "127.0.0.1"
)

// Cache is the interface for the port-forward cache.
type Cache interface {
	EnsurePortForward(service, namespace string) (string, int, error)
	EnsurePortForwardWithHint(service, namespace string, preferredPort int) (string, int, error)
	GetPortForwardIP() string
	ForceRefreshPortForward(service, namespace string) error
	Stop()
}

// sessionData holds port-forward session information.
type sessionData struct {
	LocalIP   string
	LocalPort int
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

// cacheImpl implements the Cache interface.
type cacheImpl struct {
	sync.RWMutex
	sessions      map[string]*sessionData
	monitor       *health.Monitor
	config        *config.Config
	portForwardIP string
	ipManager     *tools.IPManager
	client        kubernetes.Interface
}

// NewCache creates a new cache with default settings.
func NewCache(m *health.Monitor, cfg *config.Config) Cache {
	impl := &cacheImpl{
		sessions:  make(map[string]*sessionData),
		monitor:   m,
		config:    cfg,
		ipManager: tools.GetIPManager(),
	}

	if cfg.Network.PortForwardBindIP != "" {
		impl.portForwardIP = cfg.Network.PortForwardBindIP
		logger.Log.Debugf("Using configured port forward IP: %s", impl.portForwardIP)
	} else {
		// Determine port forward IP based on configuration
		switch {
		case cfg.Network.UseVirtualInterface && cfg.Network.PortForwardInterfaceIP != "":
			impl.portForwardIP = cfg.Network.PortForwardInterfaceIP
			logger.Log.Debugf("Using port-forward interface IP: %s", impl.portForwardIP)
		case cfg.Network.UseVirtualInterface && cfg.Network.VirtualInterfaceIP != "":
			// Fall back to DNS virtual interface IP
			impl.portForwardIP = cfg.Network.VirtualInterfaceIP
			logger.Log.Debugf("Using DNS virtual interface IP for port forwards: %s", impl.portForwardIP)
		default:
			impl.portForwardIP = localhostIP
			logger.Log.Debug("Using localhost for port forwards: 127.0.0.1")
		}
	}

	return impl
}

// NewCacheWithIP creates a new cache with a predefined IP for port forwards.
func NewCacheWithIP(m *health.Monitor, cfg *config.Config, virtualIP string) Cache {
	impl := &cacheImpl{
		sessions:  make(map[string]*sessionData),
		monitor:   m,
		config:    cfg,
		ipManager: tools.GetIPManager(),
	}

	// If virtual interface IP is provided, use it directly
	if virtualIP != "" {
		impl.portForwardIP = virtualIP
		logger.Log.Debugf("Using virtual interface IP for port forwards: %s", impl.portForwardIP)
		return impl
	}

	// Otherwise, use the same logic as NewCache
	if cfg.Network.PortForwardBindIP != "" {
		impl.portForwardIP = cfg.Network.PortForwardBindIP
		logger.Log.Debugf("Using configured port forward IP: %s", impl.portForwardIP)
	} else {
		// Determine port forward IP based on configuration
		switch {
		case cfg.Network.UseVirtualInterface && cfg.Network.PortForwardInterfaceIP != "":
			impl.portForwardIP = cfg.Network.PortForwardInterfaceIP
			logger.Log.Debugf("Using port-forward interface IP: %s", impl.portForwardIP)
		case cfg.Network.UseVirtualInterface && cfg.Network.VirtualInterfaceIP != "":
			// Fall back to DNS virtual interface IP
			impl.portForwardIP = cfg.Network.VirtualInterfaceIP
			logger.Log.Debugf("Using DNS virtual interface IP for port forwards: %s", impl.portForwardIP)
		default:
			impl.portForwardIP = localhostIP
			logger.Log.Debug("Using localhost for port forwards: 127.0.0.1")
		}
	}

	return impl
}

// GetPortForwardIP returns the IP used for port forwarding.
func (c *cacheImpl) GetPortForwardIP() string {
	c.RLock()
	defer c.RUnlock()
	return c.portForwardIP
}

// EnsurePortForward creates or reuses a port-forward to the given service.
func (c *cacheImpl) EnsurePortForward(service, namespace string) (string, int, error) {
	return c.EnsurePortForwardWithHint(service, namespace, 0)
}

// EnsurePortForwardWithHint creates or reuses a port-forward to the given service with a preferred port.
func (c *cacheImpl) EnsurePortForwardWithHint(
	service, namespace string,
	preferredPort int,
) (string, int, error) {
	startTime := time.Now()
	key := fmt.Sprintf("%s.%s", service, namespace)

	c.Lock()
	defer c.Unlock()

	if ip, port, ok := c.tryReusePortForward(key, service, namespace); ok {
		return ip, port, nil
	}

	localIP, localPort, cancel, err := c.setupPortForwardWithHint(
		key,
		service,
		namespace,
		preferredPort,
	)
	if err != nil {
		return "", 0, err
	}

	c.sessions[key] = &sessionData{
		LocalIP:   localIP,
		LocalPort: localPort,
		Cancel:    cancel,
		LastUsed:  time.Now(),
	}
	if c.monitor != nil {
		c.monitor.RegisterService(key, localPort)
	}
	c.validatePortForward(localIP, localPort)
	go c.autoExpire(key)

	logger.LogDebug("Port-forward setup completed", logrus.Fields{
		"service": service, "namespace": namespace, "local_ip": localIP, "local_port": localPort,
		"total_setup_ms": time.Since(startTime).Milliseconds(),
	})

	logger.Log.WithFields(logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"local_ip":   localIP,
		"local_port": localPort,
	}).Info("📡 Port-forward session active")

	return localIP, localPort, nil
}

func (c *cacheImpl) autoExpire(key string) {
	// Fixed infinite loop by using select with timeout and max lifetime
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Add a maximum lifetime to prevent infinite loops
	maxLifetime := time.NewTimer(24 * time.Hour) // Auto-cleanup after 24 hours max
	defer maxLifetime.Stop()

	for {
		select {
		case <-ticker.C:
			if c.checkAndExpireIdleSession(key) {
				return
			}
		case <-maxLifetime.C:
			// Force cleanup after max lifetime
			c.cleanupSession(key, "🕐 Force expiring port-forward after max lifetime", true)
			return
		}
	}
}

func (c *cacheImpl) tryReusePortForward(key, service, namespace string) (string, int, bool) {
	if pf, ok := c.sessions[key]; ok {
		logger.Log.WithFields(logrus.Fields{
			"service":    service,
			"namespace":  namespace,
			"local_ip":   pf.LocalIP,
			"local_port": pf.LocalPort,
		}).Info("♻️  Reusing existing port-forward")
		pf.LastUsed = time.Now()
		if c.monitor == nil || c.monitor.IsHealthy(key).IsHealthy {
			return pf.LocalIP, pf.LocalPort, true
		}
		if c.validateConnection(pf.LocalIP, pf.LocalPort) == nil {
			return pf.LocalIP, pf.LocalPort, true
		}
		logger.Log.WithFields(logrus.Fields{
			"service":    service,
			"namespace":  namespace,
			"local_ip":   pf.LocalIP,
			"local_port": pf.LocalPort,
		}).Warn("💔 Cached port-forward is dead, removing from cache")
		pf.Cancel()
		// Release the IP back to the manager if it was allocated
		if pf.LocalIP != localhostIP {
			c.ipManager.ReleaseLocalIP(pf.LocalIP)
		}
		delete(c.sessions, key)
		if c.monitor != nil {
			c.monitor.UnregisterService(key)
		}
	}
	return "", 0, false
}

func (c *cacheImpl) setupPortForwardWithHint(
	_ string, service, namespace string,
	preferredPort int,
) (string, int, context.CancelFunc, error) {
	// Prefer the configured port-forward IP
	localIP := c.portForwardIP
	logger.Log.WithFields(logrus.Fields{
		"service":   service,
		"namespace": namespace,
		"local_ip":  localIP,
	}).Debug("Setting up port-forward")

	// Handle port allocation
	localPort, err := c.allocatePort(localIP, preferredPort)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to allocate port: %w", err)
	}

	logger.Log.WithFields(logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"local_ip":   localIP,
		"local_port": localPort,
	}).Info("🔍 Allocated free port for port-forward")

	// Find pod and target port for the service
	podName, targetPort, config, err := c.findPodForService(service, namespace)
	if err != nil {
		return "", 0, nil, err
	}

	// Create context with cancel for port-forward
	ctx, cancel := context.WithCancel(context.Background())

	// Start port forwarding
	go c.runPortForward(ctx, config, service, namespace, podName, localIP, localPort, targetPort)

	// Wait for port-forward to be ready
	if c.waitForPortForward(service, namespace, localIP, localPort) {
		return localIP, localPort, cancel, nil
	}

	// Continue anyway even if we can't validate the connection
	logger.Log.WithFields(logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"local_ip":   localIP,
		"local_port": localPort,
	}).Warn("⚠️ Port-forward validation timeout - proceeding anyway")

	return localIP, localPort, cancel, nil
}

func (c *cacheImpl) validatePortForward(ip string, port int) {
	retries := 0
	maxRetries := 3
	start := time.Now()

	for retries < maxRetries {
		if err := c.validateConnection(ip, port); err == nil {
			logger.LogDebug("Port-forward validation successful", logrus.Fields{
				"ip":          ip,
				"port":        port,
				"retries":     retries,
				"duration_ms": time.Since(start).Milliseconds(),
			})
			return
		}
		retries++
		time.Sleep(500 * time.Millisecond)
	}

	logger.Log.WithFields(
		logrus.Fields{
			"ip":          ip,
			"port":        port,
			"retries":     retries,
			"duration_ms": time.Since(start).Milliseconds(),
		},
	).Warn("⚠️  Port-forward validation failed after retries, proceeding anyway")
}

// Stop cleans up all port-forwards.
func (c *cacheImpl) Stop() {
	c.Lock()
	defer c.Unlock()

	for key, session := range c.sessions {
		logger.Log.WithFields(logrus.Fields{
			"session":    key,
			"local_ip":   session.LocalIP,
			"local_port": session.LocalPort,
		}).Debug("Stopping port-forward")
		session.Cancel()

		// Return IP back to pool
		if session.LocalIP != localhostIP {
			c.ipManager.ReleaseLocalIP(session.LocalIP)
		}

		if c.monitor != nil {
			c.monitor.UnregisterService(key)
		}
	}

	c.sessions = make(map[string]*sessionData)
}

func (c *cacheImpl) validateConnection(ip string, port int) error {
	return tools.ValidateConnection(ip, port)
}

// allocatePort allocates an available port, trying to use the preferred port if possible.
func (c *cacheImpl) allocatePort(ip string, preferredPort int) (int, error) {
	// Check if preferred port is valid and available
	if preferredPort > 0 && preferredPort <= 65535 {
		if tools.IsPortAvailableOnIP(ip, preferredPort) {
			return preferredPort, nil
		}

		logger.Log.WithFields(logrus.Fields{
			"preferred_port": preferredPort,
			"ip":             ip,
		}).Debug("Preferred port unavailable, allocating free port")
	}

	// Allocate a random free port
	return tools.GetFreePortOnIP(ip)
}

// findPodForService looks up a pod for the given service and returns pod name, target port and K8s config.
func (c *cacheImpl) findPodForService(
	service, namespace string,
) (string, int, *rest.Config, error) {
	// Look up the pod to forward to
	config, err := k8s.GetKubeConfig()
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	// Store client for reuse
	c.client = clientset

	logger.Log.WithFields(logrus.Fields{
		"service":   service,
		"namespace": namespace,
	}).Info("Looking up service: " + namespace + "/" + service)

	podName, targetPort, err := k8s.GetPodNameForService(clientset, namespace, service)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to lookup pods for service %s.%s: %w",
			service, namespace, err)
	}

	if podName == "" {
		return "", 0, nil, fmt.Errorf("no pods found for service %s.%s", service, namespace)
	}

	return podName, targetPort, config, nil
}

// runPortForward starts the port forwarding process in a goroutine.
func (c *cacheImpl) runPortForward(
	ctx context.Context,
	config *rest.Config,
	service, namespace, podName, localIP string,
	localPort, targetPort int,
) {
	logger.Log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"local_ip":    localIP,
		"local_port":  localPort,
		"remote_port": targetPort,
		"pod":         podName,
	}).Info("🚀 Starting port-forward tunnel")

	err := k8s.StartPortForwardOnIP(ctx, config, namespace, podName, localIP, localPort, targetPort)
	if err != nil {
		logger.LogError(
			fmt.Sprintf("Port-forward for %s/%s terminated with error", namespace, service),
			err,
		)
	}
}

// waitForPortForward waits for the port-forward to be ready.
// Returns true if connection validated successfully, false otherwise.
func (c *cacheImpl) waitForPortForward(service, namespace, localIP string, localPort int) bool {
	startTime := time.Now()
	for time.Since(startTime) < 3*time.Second {
		if c.validateConnection(localIP, localPort) == nil {
			logger.Log.WithFields(logrus.Fields{
				"service":    service,
				"namespace":  namespace,
				"local_ip":   localIP,
				"local_port": localPort,
				"setup_ms":   time.Since(startTime).Milliseconds(),
			}).Info("✅ Port-forward tunnel ready")
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
} // Helper methods for autoExpire function to reduce cognitive complexity

// checkAndExpireIdleSession checks if a session is idle and expires it if necessary.
// Returns true if the session was expired or doesn't exist.
func (c *cacheImpl) checkAndExpireIdleSession(key string) bool {
	c.Lock()
	defer c.Unlock()

	session, ok := c.sessions[key]
	if !ok {
		return true // Session no longer exists, signal to exit
	}

	if time.Since(session.LastUsed) > idleTimeout {
		c.cleanupSessionLocked(session, key, "⏰ Expiring idle port-forward", false)
		return true // Signal to exit
	}

	return false // Continue monitoring
}

// cleanupSession acquires the lock and performs cleanup.
func (c *cacheImpl) cleanupSession(key string, logMessage string, isWarning bool) {
	c.Lock()
	defer c.Unlock()

	session, ok := c.sessions[key]
	if !ok {
		return
	}

	c.cleanupSessionLocked(session, key, logMessage, isWarning)
}

// cleanupSessionLocked performs the actual cleanup (assumes lock is held).
func (c *cacheImpl) cleanupSessionLocked(
	session *sessionData,
	key string,
	logMessage string,
	isWarning bool,
) {
	logFields := logrus.Fields{
		"session":    key,
		"local_ip":   session.LocalIP,
		"local_port": session.LocalPort,
	}

	if time.Since(session.LastUsed) > idleTimeout {
		logFields["idle_mins"] = int(time.Since(session.LastUsed).Minutes())
	}

	if isWarning {
		logger.Log.WithFields(logFields).Warn(logMessage)
	} else {
		logger.Log.WithFields(logFields).Info(logMessage)
	}

	session.Cancel()

	// Release the IP back to the manager if it was allocated
	if session.LocalIP != localhostIP {
		c.ipManager.ReleaseLocalIP(session.LocalIP)
	}

	delete(c.sessions, key)

	if c.monitor != nil {
		c.monitor.UnregisterService(key)
	}
}

// ForceRefreshPortForward forces a refresh of the port-forward for a service.
func (c *cacheImpl) ForceRefreshPortForward(service, namespace string) error {
	key := fmt.Sprintf("%s.%s", service, namespace)

	c.Lock()
	defer c.Unlock()

	// Stop the existing session if it exists
	if session, exists := c.sessions[key]; exists {
		logger.LogDebug("Force refreshing port-forward", logrus.Fields{
			"service":    service,
			"namespace":  namespace,
			"local_ip":   session.LocalIP,
			"local_port": session.LocalPort,
		})

		// Cancel the existing session
		if session.Cancel != nil {
			session.Cancel()
		}

		// Remove from sessions map
		delete(c.sessions, key)
	}

	// The next call to EnsurePortForward will create a fresh session
	return nil
}
