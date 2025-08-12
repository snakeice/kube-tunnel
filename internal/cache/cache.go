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

type Cache interface {
	EnsurePortForward(service, namespace string) (int, error)
}

type cacheImpl struct {
	sync.RWMutex
	sessions map[string]*PortForwardSession
	monitor  *health.Monitor
}

type PortForwardSession struct {
	LocalPort int
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

func NewCache(m *health.Monitor) Cache {
	return &cacheImpl{sessions: make(map[string]*PortForwardSession), monitor: m}
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
	if c.monitor != nil {
		c.monitor.RegisterService(key, localPort)
	}
	validatePortForward(localPort)
	go c.autoExpire(key)

	logger.LogDebug("Port-forward setup completed", logrus.Fields{
		"service": service, "namespace": namespace, "local_port": localPort,
		"total_setup_ms": time.Since(startTime).Milliseconds(),
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
			if c.monitor != nil {
				c.monitor.UnregisterService(key)
			}
			c.Unlock()
			return
		}
		c.Unlock()
	}
}

func (c *cacheImpl) tryReusePortForward(key, service, namespace string) (int, bool) {
	if pf, ok := c.sessions[key]; ok {
		logger.LogPortForwardReuse(service, namespace, pf.LocalPort)
		pf.LastUsed = time.Now()
		if c.monitor == nil || c.monitor.IsHealthy(key).IsHealthy {
			return pf.LocalPort, true
		}
		if validateConnection(pf.LocalPort) == nil {
			return pf.LocalPort, true
		}
		logger.LogDebug(
			"Cached port-forward is dead, removing from cache",
			logrus.Fields{"service": service, "namespace": namespace, "port": pf.LocalPort},
		)
		pf.Cancel()
		delete(c.sessions, key)
		if c.monitor != nil {
			c.monitor.UnregisterService(key)
		}
	}
	return 0, false
}

func setupPortForward(
	key, service, namespace string,
	startTime time.Time,
) (int, context.CancelFunc, error) {
	localPort, err := tools.GetFreePort()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get free port: %w", err)
	}
	cfg, err := k8s.GetKubeConfig()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get kube config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	podName, remotePort, err := k8s.GetPodNameForService(clientset, namespace, service)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get pod for service %s: %w", key, err)
	}
	setupDuration := time.Since(startTime)
	logger.LogPortForwardWithTiming(
		service, namespace, podName, localPort, remotePort, setupDuration,
	)
	ctx, cancel := context.WithCancel(context.Background())
	pfStart := time.Now()
	go func() {
		if err := k8s.StartPortForward(ctx, cfg, namespace, podName, localPort, remotePort); err != nil {
			logger.LogPortForwardError(key, err, time.Since(pfStart))
			return
		}
		logger.LogDebug(
			"Port-forward established successfully",
			logrus.Fields{
				"service":     service,
				"namespace":   namespace,
				"duration_ms": time.Since(pfStart).Milliseconds(),
			},
		)
	}()
	return localPort, cancel, nil
}

func validatePortForward(localPort int) {
	start := time.Now()
	maxRetries := 10
	baseDelay := 100 * time.Millisecond
	for attempt := range maxRetries { // Go 1.22 intrange
		if err := validateConnection(localPort); err == nil {
			logger.LogDebug(
				"Port-forward validation successful",
				logrus.Fields{
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
				"port":        localPort,
				"attempts":    maxRetries,
				"duration_ms": time.Since(start).Milliseconds(),
			},
		).Warn("Port-forward validation failed after retries, proceeding anyway")
	}
}

func validateConnection(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
	if err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		logger.LogError("Failed to close connection", err)
	}
	return nil
}
