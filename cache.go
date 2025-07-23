package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
)

const idleTimeout = 10 * time.Minute

type PortForwardSession struct {
	LocalPort int
	Cancel    context.CancelFunc
	LastUsed  time.Time
}

var (
	cache     = map[string]*PortForwardSession{}
	cacheLock = sync.Mutex{}
)

func ensurePortForward(service, namespace string, remotePort int) (int, error) {
	key := fmt.Sprintf("%s.%s", service, namespace)
	log.Printf("Ensuring port-forward for %s (remote port: %d)", key, remotePort)

	cacheLock.Lock()
	defer cacheLock.Unlock()

	if pf, ok := cache[key]; ok {
		log.Printf("Cache hit for %s, reusing local port %d (last used: %v ago)",
			key, pf.LocalPort, time.Since(pf.LastUsed).Round(time.Second))
		pf.LastUsed = time.Now()
		return pf.LocalPort, nil
	}

	log.Printf("Cache miss for %s, creating new port-forward", key)

	localPort, err := getFreePort()
	if err != nil {
		log.Printf("Failed to get free port for %s: %v", key, err)
		return 0, err
	}
	log.Printf("Allocated local port %d for %s", localPort, key)

	config, err := getKubeConfig()
	if err != nil {
		log.Printf("Failed to get kube config for %s: %v", key, err)
		return 0, err
	}
	log.Printf("Got Kubernetes config for %s", key)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("Failed to create Kubernetes client for %s: %v", key, err)
		return 0, err
	}

	podName, err := getPodNameForService(clientset, namespace, service)
	if err != nil {
		log.Printf("Failed to get pod name for service %s: %v", key, err)
		return 0, err
	}
	log.Printf("Found pod %s for service %s", podName, key)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		log.Printf("Starting port-forward: %s (pod: %s, %d:%d)", key, podName, localPort, remotePort)
		err := startPortForward(ctx, config, namespace, podName, localPort, remotePort)
		if err != nil {
			log.Printf("Port-forward error %s: %v", key, err)
		} else {
			log.Printf("Port-forward ended cleanly for %s", key)
		}
	}()

	cache[key] = &PortForwardSession{
		LocalPort: localPort,
		Cancel:    cancel,
		LastUsed:  time.Now(),
	}
	log.Printf("Cached new port-forward session for %s (local port: %d)", key, localPort)
	time.Sleep(1 * time.Second) // Ensure port-forward is established before returning

	go autoExpire(key)

	return localPort, nil
}

func autoExpire(key string) {
	log.Printf("Started auto-expire monitoring for %s (timeout: %v)", key, idleTimeout)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cacheLock.Lock()
		session, ok := cache[key]
		if !ok {
			log.Printf("Auto-expire: session %s no longer exists, stopping monitor", key)
			cacheLock.Unlock()
			return
		}

		idleTime := time.Since(session.LastUsed)
		if idleTime > idleTimeout {
			log.Printf("Stopping idle port-forward for %s (idle for %v)", key, idleTime.Round(time.Second))
			session.Cancel()
			delete(cache, key)
			cacheLock.Unlock()
			return
		}

		log.Printf("Auto-expire check: %s still active (idle for %v)", key, idleTime.Round(time.Second))
		cacheLock.Unlock()
	}
}
