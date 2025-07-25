package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// HealthStatus represents the health state of a backend service
type HealthStatus struct {
	IsHealthy    bool
	LastChecked  time.Time
	LastHealthy  time.Time
	FailureCount int
	ResponseTime time.Duration
	ErrorMessage string
}

// HealthMonitor manages background health checks for all active services
type HealthMonitor struct {
	healthCache   map[string]*HealthStatus
	cacheLock     sync.RWMutex
	checkInterval time.Duration
	timeout       time.Duration
	maxFailures   int
	enabled       bool
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

// HealthConfig holds configuration for health monitoring
type HealthConfig struct {
	Enabled         bool
	CheckInterval   time.Duration
	Timeout         time.Duration
	MaxFailures     int
	RecoveryRetries int
}

var (
	globalHealthMonitor *HealthMonitor
	healthConfig        = loadHealthConfig()
)

// loadHealthConfig loads health monitoring configuration from environment
func loadHealthConfig() HealthConfig {
	config := HealthConfig{
		Enabled:         true,
		CheckInterval:   30 * time.Second,
		Timeout:         2 * time.Second,
		MaxFailures:     3,
		RecoveryRetries: 2,
	}

	if os.Getenv("HEALTH_MONITOR_ENABLED") == "false" {
		config.Enabled = false
	}

	if val := os.Getenv("HEALTH_CHECK_INTERVAL"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.CheckInterval = parsed
		}
	}

	if val := os.Getenv("HEALTH_CHECK_TIMEOUT"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.Timeout = parsed
		}
	}

	if val := os.Getenv("HEALTH_MAX_FAILURES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			config.MaxFailures = parsed
		}
	}

	if val := os.Getenv("HEALTH_RECOVERY_RETRIES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			config.RecoveryRetries = parsed
		}
	}

	return config
}

// NewHealthMonitor creates a new health monitor instance
func NewHealthMonitor(config HealthConfig) *HealthMonitor {
	return &HealthMonitor{
		healthCache:   make(map[string]*HealthStatus),
		checkInterval: config.CheckInterval,
		timeout:       config.Timeout,
		maxFailures:   config.MaxFailures,
		enabled:       config.Enabled,
		stopChan:      make(chan struct{}),
	}
}

// Start begins background health monitoring
func (hm *HealthMonitor) Start() {
	if !hm.enabled {
		LogDebug("Health monitor disabled", logrus.Fields{})
		return
	}

	LogDebug("Starting background health monitor", logrus.Fields{
		"interval":     hm.checkInterval,
		"timeout":      hm.timeout,
		"max_failures": hm.maxFailures,
	})

	hm.wg.Add(1)
	go hm.monitorLoop()
}

// Stop gracefully stops the health monitor
func (hm *HealthMonitor) Stop() {
	if !hm.enabled {
		return
	}

	LogDebug("Stopping health monitor", logrus.Fields{})
	close(hm.stopChan)
	hm.wg.Wait()
}

// RegisterService adds a service to health monitoring
func (hm *HealthMonitor) RegisterService(serviceKey string, port int32) {
	if !hm.enabled {
		return
	}

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	if _, exists := hm.healthCache[serviceKey]; !exists {
		hm.healthCache[serviceKey] = &HealthStatus{
			IsHealthy:    true, // Assume healthy initially
			LastChecked:  time.Now(),
			LastHealthy:  time.Now(),
			FailureCount: 0,
			ResponseTime: 0,
		}

		LogDebug("Registered service for health monitoring", logrus.Fields{
			"service": serviceKey,
			"port":    port,
		})

		// Perform initial health check
		go hm.checkServiceHealth(serviceKey, port)
	}
}

// UnregisterService removes a service from health monitoring
func (hm *HealthMonitor) UnregisterService(serviceKey string) {
	if !hm.enabled {
		return
	}

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	if _, exists := hm.healthCache[serviceKey]; exists {
		delete(hm.healthCache, serviceKey)
		LogDebug("Unregistered service from health monitoring", logrus.Fields{
			"service": serviceKey,
		})
	}
}

// IsHealthy returns the current health status of a service
func (hm *HealthMonitor) IsHealthy(serviceKey string) (bool, *HealthStatus) {
	if !hm.enabled {
		return true, &HealthStatus{IsHealthy: true}
	}

	hm.cacheLock.RLock()
	defer hm.cacheLock.RUnlock()

	if status, exists := hm.healthCache[serviceKey]; exists {
		// Consider service stale if not checked recently
		staleDuration := hm.checkInterval * 3
		if time.Since(status.LastChecked) > staleDuration {
			LogDebug("Service health status is stale", logrus.Fields{
				"service":      serviceKey,
				"last_checked": status.LastChecked,
				"stale_after":  staleDuration,
			})
			return false, status
		}
		return status.IsHealthy, status
	}

	// Service not monitored, assume healthy
	return true, &HealthStatus{IsHealthy: true}
}

// GetAllHealthStatus returns health status for all monitored services
func (hm *HealthMonitor) GetAllHealthStatus() map[string]*HealthStatus {
	if !hm.enabled {
		return make(map[string]*HealthStatus)
	}

	hm.cacheLock.RLock()
	defer hm.cacheLock.RUnlock()

	result := make(map[string]*HealthStatus)
	for key, status := range hm.healthCache {
		// Create a copy to avoid data races
		result[key] = &HealthStatus{
			IsHealthy:    status.IsHealthy,
			LastChecked:  status.LastChecked,
			LastHealthy:  status.LastHealthy,
			FailureCount: status.FailureCount,
			ResponseTime: status.ResponseTime,
			ErrorMessage: status.ErrorMessage,
		}
	}

	return result
}

// monitorLoop runs the main health monitoring loop
func (hm *HealthMonitor) monitorLoop() {
	defer hm.wg.Done()

	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopChan:
			return
		case <-ticker.C:
			hm.performHealthChecks()
		}
	}
}

// performHealthChecks checks health of all registered services
func (hm *HealthMonitor) performHealthChecks() {
	hm.cacheLock.RLock()
	servicesToCheck := make(map[string]int32)

	// Get active port-forwards from cache
	cacheLock.Lock()
	for key, session := range cache {
		// Only check services that have active port-forwards
		if session != nil {
			servicesToCheck[key] = session.LocalPort
		}
	}
	cacheLock.Unlock()
	hm.cacheLock.RUnlock()

	LogDebug("Performing health checks", logrus.Fields{
		"service_count": len(servicesToCheck),
	})

	// Check each service in parallel
	var wg sync.WaitGroup
	for serviceKey, port := range servicesToCheck {
		wg.Add(1)
		go func(key string, p int32) {
			defer wg.Done()
			hm.checkServiceHealth(key, p)
		}(serviceKey, port)
	}

	wg.Wait()
}

// checkServiceHealth performs a health check on a specific service
func (hm *HealthMonitor) checkServiceHealth(serviceKey string, port int32) {
	startTime := time.Now()

	// Perform multiple health check types
	isHealthy, err := hm.performHealthCheck(port)
	responseTime := time.Since(startTime)

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	status, exists := hm.healthCache[serviceKey]
	if !exists {
		status = &HealthStatus{}
		hm.healthCache[serviceKey] = status
	}

	status.LastChecked = time.Now()
	status.ResponseTime = responseTime

	if isHealthy {
		if !status.IsHealthy {
			LogDebug("Service recovered", logrus.Fields{
				"service":     serviceKey,
				"port":        port,
				"response_ms": responseTime.Milliseconds(),
				"was_failing": status.FailureCount,
			})
		}
		status.IsHealthy = true
		status.LastHealthy = time.Now()
		status.FailureCount = 0
		status.ErrorMessage = ""
	} else {
		status.FailureCount++
		if err != nil {
			status.ErrorMessage = err.Error()
		}

		// Mark as unhealthy after max failures
		if status.FailureCount >= hm.maxFailures {
			if status.IsHealthy {
				LogDebug("Service marked unhealthy", logrus.Fields{
					"service":       serviceKey,
					"port":          port,
					"failure_count": status.FailureCount,
					"error":         status.ErrorMessage,
				})
			}
			status.IsHealthy = false
		}
	}

	// Log periodic health status for debugging
	if status.FailureCount > 0 || responseTime > 100*time.Millisecond {
		LogDebug("Health check result", logrus.Fields{
			"service":       serviceKey,
			"port":          port,
			"healthy":       isHealthy,
			"response_ms":   responseTime.Milliseconds(),
			"failure_count": status.FailureCount,
			"error":         status.ErrorMessage,
		})
	}
}

// performHealthCheck executes the actual health check
func (hm *HealthMonitor) performHealthCheck(port int32) (bool, error) {
	// Try TCP connection first (fastest)
	if err := hm.checkTCPConnection(port); err != nil {
		return false, fmt.Errorf("TCP check failed: %v", err)
	}

	// Try HTTP request (more thorough)
	if err := hm.checkHTTPEndpoint(port); err != nil {
		// TCP works but HTTP doesn't - might be non-HTTP service
		LogDebug("HTTP check failed, but TCP is working", logrus.Fields{
			"port":  port,
			"error": err.Error(),
		})
		// Consider it healthy if TCP works (might be gRPC, database, etc.)
		return true, nil
	}

	return true, nil
}

// checkTCPConnection performs a simple TCP connection test
func (hm *HealthMonitor) checkTCPConnection(port int32) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), hm.timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// checkHTTPEndpoint performs an HTTP health check
func (hm *HealthMonitor) checkHTTPEndpoint(port int32) error {
	client := &http.Client{
		Timeout: hm.timeout,
		Transport: &http.Transport{
			DisableKeepAlives:   true,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 1,
		},
	}

	// Try common health check endpoints
	endpoints := []string{"/health", "/healthz", "/ready", "/ping", "/"}

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("http://localhost:%d%s", port, endpoint)
		resp, err := client.Get(url)
		if err != nil {
			continue // Try next endpoint
		}
		resp.Body.Close()

		// Accept any HTTP response as healthy (even 404)
		if resp.StatusCode < 500 {
			return nil
		}
	}

	return fmt.Errorf("all HTTP endpoints returned 5xx errors")
}

// GetHealthMonitor returns the global health monitor instance
func GetHealthMonitor() *HealthMonitor {
	if globalHealthMonitor == nil {
		globalHealthMonitor = NewHealthMonitor(healthConfig)
	}
	return globalHealthMonitor
}

// InitializeHealthMonitor initializes and starts the global health monitor
func InitializeHealthMonitor() {
	if globalHealthMonitor == nil {
		globalHealthMonitor = NewHealthMonitor(healthConfig)
		LogDebug("Health monitor initialized", logrus.Fields{
			"enabled":      healthConfig.Enabled,
			"interval":     healthConfig.CheckInterval,
			"timeout":      healthConfig.Timeout,
			"max_failures": healthConfig.MaxFailures,
		})
	}
	if healthConfig.Enabled && globalHealthMonitor != nil {
		globalHealthMonitor.Start()
		LogDebug("Health monitor started successfully", logrus.Fields{})
	} else if !healthConfig.Enabled {
		LogDebug("Health monitor disabled by configuration", logrus.Fields{})
	}
}

// isBackendHealthy is a convenience function for the proxy
func isBackendHealthy(serviceKey string) bool {
	if globalHealthMonitor == nil {
		return true // Assume healthy if monitor not initialized
	}
	healthy, _ := globalHealthMonitor.IsHealthy(serviceKey)
	return healthy
}

// registerServiceForMonitoring registers a service when port-forward is created
func registerServiceForMonitoring(serviceKey string, port int32) {
	if globalHealthMonitor != nil {
		globalHealthMonitor.RegisterService(serviceKey, port)
	}
}

// unregisterServiceFromMonitoring removes a service when port-forward is cleaned up
func unregisterServiceFromMonitoring(serviceKey string) {
	if globalHealthMonitor != nil {
		globalHealthMonitor.UnregisterService(serviceKey)
	}
}
