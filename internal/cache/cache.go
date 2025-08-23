package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

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

type Cache interface {
	EnsurePortForward(service, namespace string) (string, int, error)
	EnsurePortForwardWithHint(service, namespace string, preferredPort int) (string, int, error)
	GetPortForwardIP() string
}

type cacheImpl struct {
	sync.RWMutex
	sessions      map[string]*PortForwardSession
	monitor       *health.Monitor
	config        *config.Config
	portForwardIP string
	ipManager     *tools.IPManager
}

type PortForwardSession struct {
	LocalIP   string
	LocalPort int
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

// NewCacheWithIP creates a new cache with a predefined IP for port forwards.
func NewCacheWithIP(m *health.Monitor, cfg *config.Config, virtualIP string) Cache {
	impl := &cacheImpl{
		sessions:  make(map[string]*PortForwardSession),
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
		// Use virtual interface IP if available, otherwise localhost
		if cfg.Network.UseVirtualInterface && cfg.Network.VirtualInterfaceIP != "" {
			impl.portForwardIP = cfg.Network.VirtualInterfaceIP
			logger.Log.Debugf("Using virtual interface IP for port forwards: %s", impl.portForwardIP)
		} else {
			impl.portForwardIP = localhostIP
			logger.Log.Debug("Using localhost for port forwards: 127.0.0.1")
		}
	}

	return impl
}

func (c *cacheImpl) GetPortForwardIP() string {
	c.RLock()
	defer c.RUnlock()
	return c.portForwardIP
}

func (c *cacheImpl) EnsurePortForward(service, namespace string) (string, int, error) {
	return c.EnsurePortForwardWithHint(service, namespace, 0)
}

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
		startTime,
		preferredPort,
	)
	if err != nil {
		return "", 0, err
	}

	c.sessions[key] = &PortForwardSession{
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
	return localIP, localPort, nil
}

func (c *cacheImpl) autoExpire(key string) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.Lock()
		session, ok := c.sessions[key]
		if !ok {
			c.Unlock()
			return
		}
		if time.Since(session.LastUsed) > idleTimeout {
			logger.LogPortForwardExpire(key)
			session.Cancel()
			// Release the IP back to the manager if it was allocated
			if session.LocalIP != "127.0.0.1" {
				c.ipManager.ReleaseLocalIP(session.LocalIP)
			}
			delete(c.sessions, key)
			if c.monitor != nil {
				c.monitor.UnregisterService(key)
			}
			c.Unlock()
			return
		}
		c.Unlock()
	}
}

func (c *cacheImpl) tryReusePortForward(key, service, namespace string) (string, int, bool) {
	if pf, ok := c.sessions[key]; ok {
		logger.LogPortForwardReuse(service, namespace, pf.LocalPort)
		pf.LastUsed = time.Now()
		if c.monitor == nil || c.monitor.IsHealthy(key).IsHealthy {
			return pf.LocalIP, pf.LocalPort, true
		}
		if c.validateConnection(pf.LocalIP, pf.LocalPort) == nil {
			return pf.LocalIP, pf.LocalPort, true
		}
		logger.LogDebug(
			"Cached port-forward is dead, removing from cache",
			logrus.Fields{
				"service":   service,
				"namespace": namespace,
				"ip":        pf.LocalIP,
				"port":      pf.LocalPort,
			},
		)
		pf.Cancel()
		// Release the IP back to the manager if it was allocated
		if pf.LocalIP != "127.0.0.1" {
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
	key, service, namespace string,
	startTime time.Time,
	preferredPort int,
) (string, int, context.CancelFunc, error) {
	localIP := c.portForwardIP
	var localPort int
	var err error

	// Try to use the preferred port if specified and available
	if preferredPort > 0 && preferredPort <= 65535 {
		if tools.IsPortAvailableOnIP(localIP, preferredPort) {
			localPort = preferredPort
			logger.LogDebug("Using preferred port for port-forward", logrus.Fields{
				"service": service, "namespace": namespace, "port": preferredPort,
			})
		} else {
			logger.LogDebug("Preferred port not available, finding free port", logrus.Fields{
				"service": service, "namespace": namespace, "preferred_port": preferredPort,
			})
		}
	}

	// Fallback to finding a free port if preferred port is not available
	if localPort == 0 {
		localPort, err = tools.GetFreePortOnIP(localIP)
		if err != nil {
			return "", 0, nil, fmt.Errorf("failed to get free port on IP %s: %w", localIP, err)
		}
	}
	cfg, err := k8s.GetKubeConfig()
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get kube config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	podName, remotePort, err := k8s.GetPodNameForService(clientset, namespace, service)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get pod for service %s: %w", key, err)
	}
	setupDuration := time.Since(startTime)
	logger.LogDebug("Setting up port-forward", logrus.Fields{
		"service": service, "namespace": namespace, "pod": podName,
		"local_ip": localIP, "local_port": localPort, "remote_port": remotePort,
		"setup_duration_ms": setupDuration.Milliseconds(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	pfStart := time.Now()
	go func() {
		if err := k8s.StartPortForwardOnIP(ctx, cfg, namespace, podName, localIP, localPort, remotePort); err != nil {
			logger.LogError("Port-forward failed", err)
			logger.LogDebug("Port-forward error details", logrus.Fields{
				"service": service, "namespace": namespace, "duration_ms": time.Since(pfStart).Milliseconds(),
			})
			return
		}
		logger.LogDebug(
			"Port-forward established successfully",
			logrus.Fields{
				"service":     service,
				"namespace":   namespace,
				"local_ip":    localIP,
				"local_port":  localPort,
				"duration_ms": time.Since(pfStart).Milliseconds(),
			},
		)
	}()
	return localIP, localPort, cancel, nil
}

func (c *cacheImpl) validatePortForward(localIP string, localPort int) {
	start := time.Now()
	maxRetries := 10
	baseDelay := 100 * time.Millisecond
	for attempt := range maxRetries { // Go 1.22 intrange
		if err := c.validateConnection(localIP, localPort); err == nil {
			logger.LogDebug(
				"Port-forward validation successful",
				logrus.Fields{
					"ip":          localIP,
					"port":        localPort,
					"attempt":     attempt + 1,
					"duration_ms": time.Since(start).Milliseconds(),
				},
			)
			break
		}
		if attempt < maxRetries-1 {
			time.Sleep(baseDelay * time.Duration(attempt+1))
			continue
		}
		logger.Log.WithFields(
			logrus.Fields{
				"ip":          localIP,
				"port":        localPort,
				"attempts":    maxRetries,
				"duration_ms": time.Since(start).Milliseconds(),
			},
		).Warn("Port-forward validation failed after retries, proceeding anyway")
	}
}

func (c *cacheImpl) validateConnection(ip string, port int) error {
	return tools.ValidateConnection(ip, port)
}
