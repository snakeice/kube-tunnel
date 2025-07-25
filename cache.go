package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
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

func ensurePortForward(service, namespace string) (int32, error) {
	startTime := time.Now()
	key := fmt.Sprintf("%s.%s", service, namespace)

	cacheLock.Lock()
	defer cacheLock.Unlock()

	if pf, ok := cache[key]; ok {
		LogPortForwardReuse(service, namespace, pf.LocalPort)
		pf.LastUsed = time.Now()
		return pf.LocalPort, nil
	}

	localPort, err := getFreePort()
	if err != nil {
		return 0, fmt.Errorf("failed to get free port: %v", err)
	}

	config, err := getKubeConfig()
	if err != nil {
		return 0, fmt.Errorf("failed to get kube config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return 0, fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	podName, remotePort, err := getPodNameForService(clientset, namespace, service)
	if err != nil {
		return 0, fmt.Errorf("failed to get pod for service %s: %v", key, err)
	}

	setupDuration := time.Since(startTime)
	LogPortForwardWithTiming(service, namespace, podName, localPort, remotePort, setupDuration)

	ctx, cancel := context.WithCancel(context.Background())
	portForwardStartTime := time.Now()
	go func() {
		if err := startPortForward(ctx, config, namespace, podName, localPort, remotePort); err != nil {
			portForwardDuration := time.Since(portForwardStartTime)
			LogPortForwardError(key, err, portForwardDuration)
		} else {
			portForwardDuration := time.Since(portForwardStartTime)
			LogDebug("Port-forward established successfully", logrus.Fields{
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

	// Wait longer for port-forward to be fully ready
	time.Sleep(2 * time.Second)

	// Validate the connection is actually working
	validationStartTime := time.Now()
	if err := validateConnection(localPort); err != nil {
		log.WithFields(logrus.Fields{
			"port":        localPort,
			"duration_ms": time.Since(validationStartTime).Milliseconds(),
		}).Warn("Port-forward may not be fully ready")
		// Wait a bit more and try again
		time.Sleep(3 * time.Second)
		secondValidationStart := time.Now()
		if err := validateConnection(localPort); err != nil {
			log.WithFields(logrus.Fields{
				"port":        localPort,
				"error":       err.Error(),
				"duration_ms": time.Since(secondValidationStart).Milliseconds(),
			}).Warn("Port-forward validation failed, proceeding anyway")
		} else {
			log.WithFields(logrus.Fields{
				"port":        localPort,
				"duration_ms": time.Since(secondValidationStart).Milliseconds(),
			}).Debug("Port-forward validation succeeded on retry")
		}
	} else {
		log.WithFields(logrus.Fields{
			"port":        localPort,
			"duration_ms": time.Since(validationStartTime).Milliseconds(),
		}).Debug("Port-forward validation successful")
	}

	go autoExpire(key)

	totalDuration := time.Since(startTime)
	LogDebug("Port-forward setup completed", logrus.Fields{
		"service":        service,
		"namespace":      namespace,
		"local_port":     localPort,
		"total_setup_ms": totalDuration.Milliseconds(),
	})

	return localPort, nil
}

// validateConnection checks if the port-forward is actually ready
func validateConnection(port int32) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 1*time.Second)
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
			LogPortForwardExpire(key)
			session.Cancel()
			delete(cache, key)
			cacheLock.Unlock()
			return
		}
		cacheLock.Unlock()
	}
}
