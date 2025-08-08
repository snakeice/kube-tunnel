package cache

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/k8s"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

const idleTimeout = 5 * time.Minute

type (
	Cache interface {
		EnsurePortForward(service, namespace string) (int, error)
	}

	cacheImpl struct {
		sync.RWMutex
		sessions map[string]*PortForwardSession
	}
)

type PortForwardSession struct {
	LocalPort int
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

func NewCache() Cache {
	return &cacheImpl{
		sessions: make(map[string]*PortForwardSession),
	}
}

func (c *cacheImpl) EnsurePortForward(service, namespace string) (int, error) {
	startTime := time.Now()
	key := fmt.Sprintf("%s.%s", service, namespace)

	c.Lock()
	defer c.Unlock()

	if port, ok := c.tryReusePortForward(key, service, namespace); ok {
		return port, nil
	}

	localPort, cancel, err := setupPortForward(key, service, namespace, startTime)
	if err != nil {
		return 0, err
	}

	c.sessions[key] = &PortForwardSession{
		LocalPort: localPort,
		Cancel:    cancel,
		LastUsed:  time.Now(),
	}

	health.RegisterServiceForMonitoring(key, localPort)
	validatePortForward(localPort)
	go c.autoExpire(key)

	totalDuration := time.Since(startTime)
	logger.LogDebug("Port-forward setup completed", logrus.Fields{
		"service":        service,
		"namespace":      namespace,
		"local_port":     localPort,
		"total_setup_ms": totalDuration.Milliseconds(),
	})

	return localPort, nil
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
			delete(c.sessions, key)
			// Unregister from health monitoring
			health.UnregisterServiceFromMonitoring(key)
			c.Unlock()
			return
		}
		c.Unlock()
	}
}

// tryReusePortForward attempts to reuse an existing port-forward session if healthy.
func (c *cacheImpl) tryReusePortForward(key, service, namespace string) (int, bool) {
	if pf, ok := c.sessions[key]; ok {
		logger.LogPortForwardReuse(service, namespace, pf.LocalPort)
		pf.LastUsed = time.Now()

		if health.IsBackendHealthy(key) {
			return pf.LocalPort, true
		}

		if validateConnection(pf.LocalPort) == nil {
			return pf.LocalPort, true
		}

		logger.LogDebug("Cached port-forward is dead, removing from cache", logrus.Fields{
			"service":   service,
			"namespace": namespace,
			"port":      pf.LocalPort,
		})
		pf.Cancel()
		delete(c.sessions, key)
		health.UnregisterServiceFromMonitoring(key)
	}
	return 0, false
}

// setupPortForward sets up a new port-forward session and returns the local port and cancel function.
func setupPortForward(
	key, service, namespace string,
	startTime time.Time,
) (int, context.CancelFunc, error) {
	localPort, err := tools.GetFreePort()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get free port: %w", err)
	}

	config, err := k8s.GetKubeConfig()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get kube config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	podName, remotePort, err := k8s.GetPodNameForService(clientset, namespace, service)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get pod for service %s: %w", key, err)
	}

	setupDuration := time.Since(startTime)
	logger.LogPortForwardWithTiming(
		service,
		namespace,
		podName,
		localPort,
		remotePort,
		setupDuration,
	)

	ctx, cancel := context.WithCancel(context.Background())
	portForwardStartTime := time.Now()
	go func() {
		if err := k8s.StartPortForward(ctx, config, namespace, podName, localPort, remotePort); err != nil {
			portForwardDuration := time.Since(portForwardStartTime)
			logger.LogPortForwardError(key, err, portForwardDuration)
		} else {
			portForwardDuration := time.Since(portForwardStartTime)
			logger.LogDebug("Port-forward established successfully", logrus.Fields{
				"service":     service,
				"namespace":   namespace,
				"duration_ms": portForwardDuration.Milliseconds(),
			})
		}
	}()

	return localPort, cancel, nil
}

// validatePortForward validates the port-forward connection with retries.
func validatePortForward(localPort int) {
	validationStartTime := time.Now()
	maxRetries := 10
	baseDelay := 100 * time.Millisecond

	for attempt := range maxRetries {
		if err := validateConnection(localPort); err == nil {
			logger.Log.WithFields(logrus.Fields{
				"port":        localPort,
				"attempt":     attempt + 1,
				"duration_ms": time.Since(validationStartTime).Milliseconds(),
			}).Debug("Port-forward validation successful")
			break
		}

		if attempt < maxRetries-1 {
			delay := baseDelay * time.Duration(attempt+1)
			time.Sleep(delay)
		} else {
			logger.Log.WithFields(logrus.Fields{
				"port":        localPort,
				"attempts":    maxRetries,
				"duration_ms": time.Since(validationStartTime).Milliseconds(),
			}).Warn("Port-forward validation failed after retries, proceeding anyway")
		}
	}
}

// validateConnection checks if the port-forward is actually ready.
func validateConnection(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
	if err != nil {
		return err
	}
	// Ensure conn.Close() error is checked
	if err := conn.Close(); err != nil {
		logger.LogError("Failed to close connection", err)
	}
	return nil
}
