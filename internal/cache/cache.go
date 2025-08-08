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

type PortForwardSession struct {
	LocalPort int32
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

var (
	cache     = map[string]*PortForwardSession{}
	cacheLock = sync.Mutex{}
)

func EnsurePortForward(service, namespace string) (int32, error) {
	startTime := time.Now()
	key := fmt.Sprintf("%s.%s", service, namespace)

	cacheLock.Lock()
	defer cacheLock.Unlock()

	if pf, ok := cache[key]; ok {
		logger.LogPortForwardReuse(service, namespace, pf.LocalPort)
		pf.LastUsed = time.Now()

		// Check health status from background monitor first
		if health.IsBackendHealthy(key) {
			return pf.LocalPort, nil
		}

		// If health monitor reports unhealthy, do quick validation
		if validateConnection(pf.LocalPort) == nil {
			// Connection works, might be temporary health issue
			return pf.LocalPort, nil
		}

		// If validation fails, remove from cache and create new
		logger.LogDebug("Cached port-forward is dead, removing from cache", logrus.Fields{
			"service":   service,
			"namespace": namespace,
			"port":      pf.LocalPort,
		})
		pf.Cancel()
		delete(cache, key)
		health.UnregisterServiceFromMonitoring(key)
	}

	localPort, err := tools.GetFreePort()
	if err != nil {
		return 0, fmt.Errorf("failed to get free port: %w", err)
	}

	config, err := k8s.GetKubeConfig()
	if err != nil {
		return 0, fmt.Errorf("failed to get kube config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return 0, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	podName, remotePort, err := k8s.GetPodNameForService(clientset, namespace, service)
	if err != nil {
		return 0, fmt.Errorf("failed to get pod for service %s: %w", key, err)
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

	cache[key] = &PortForwardSession{
		LocalPort: localPort,
		Cancel:    cancel,
		LastUsed:  time.Now(),
	}

	// Register service for health monitoring
	health.RegisterServiceForMonitoring(key, localPort)

	// Use faster validation with shorter waits
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

	go autoExpire(key)

	totalDuration := time.Since(startTime)
	logger.LogDebug("Port-forward setup completed", logrus.Fields{
		"service":        service,
		"namespace":      namespace,
		"local_port":     localPort,
		"total_setup_ms": totalDuration.Milliseconds(),
	})

	return localPort, nil
}

// validateConnection checks if the port-forward is actually ready.
func validateConnection(port int32) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func autoExpire(key string) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cacheLock.Lock()
		session, ok := cache[key]
		if !ok {
			cacheLock.Unlock()
			return
		}

		if time.Since(session.LastUsed) > idleTimeout {
			logger.LogPortForwardExpire(key)
			session.Cancel()
			delete(cache, key)
			// Unregister from health monitoring
			health.UnregisterServiceFromMonitoring(key)
			cacheLock.Unlock()
			return
		}
		cacheLock.Unlock()
	}
}
