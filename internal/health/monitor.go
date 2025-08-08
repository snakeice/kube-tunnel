package health

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// Status represents the health state of a backend service.
type Status struct {
	IsHealthy    bool
	LastChecked  time.Time
	LastHealthy  time.Time
	FailureCount int
	ResponseTime time.Duration
	ErrorMessage string
	Port         int
}

// HealthMonitor manages background health checks for all active services.
// UnregisterServiceFromMonitoring removes a service when port-forward is cleaned up.
func UnregisterServiceFromMonitoring(serviceKey string) {
	if globalHealthMonitor != nil {
		globalHealthMonitor.UnregisterService(serviceKey)
	}
}

// StopHealthMonitor gracefully stops the health monitoring background tasks.
func StopHealthMonitor() {
	if globalHealthMonitor != nil {
		globalHealthMonitor.Stop()
	}
}

type Monitor struct {
	healthCache   map[string]*Status
	cacheLock     sync.RWMutex
	checkInterval time.Duration
	timeout       time.Duration
	maxFailures   int
	enabled       bool
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

var (
	globalHealthMonitor *Monitor
)

func NewHealthMonitor(config config.HealthConfig) *Monitor {
	return &Monitor{
		healthCache:   make(map[string]*Status),
		checkInterval: config.CheckInterval,
		timeout:       config.Timeout,
		maxFailures:   config.MaxFailures,
		enabled:       config.Enabled,
		stopChan:      make(chan struct{}),
	}
}

func (hm *Monitor) Start() {
	if !hm.enabled {
		return
	}

	logger.LogDebug("Starting background health monitor", logrus.Fields{
		"interval":     hm.checkInterval,
		"timeout":      hm.timeout,
		"max_failures": hm.maxFailures,
	})

	hm.wg.Add(1)
	go hm.monitorLoop()
}

func (hm *Monitor) Stop() {
	if !hm.enabled {
		return
	}

	close(hm.stopChan)
	hm.wg.Wait()
}

func (hm *Monitor) RegisterService(serviceKey string, port int) {
	if !hm.enabled {
		return
	}

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	if _, exists := hm.healthCache[serviceKey]; !exists {
		hm.healthCache[serviceKey] = &Status{
			IsHealthy:    true, // Assume healthy initially
			LastChecked:  time.Now(),
			LastHealthy:  time.Now(),
			FailureCount: 0,
			ResponseTime: 0,
			Port:         port,
		}

		logger.LogDebug("Registered service for health monitoring", logrus.Fields{
			"service": serviceKey,
			"port":    port,
		})

		// Perform initial health check
		go hm.checkServiceHealth(serviceKey, port)
	}
}

func (hm *Monitor) UnregisterService(serviceKey string) {
	if !hm.enabled {
		return
	}

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	if _, exists := hm.healthCache[serviceKey]; exists {
		delete(hm.healthCache, serviceKey)
		logger.LogDebug("Unregistered service from health monitoring", logrus.Fields{
			"service": serviceKey,
		})
	}
}

func (hm *Monitor) IsHealthy(serviceKey string) *Status {
	if !hm.enabled {
		return &Status{IsHealthy: true}
	}

	hm.cacheLock.RLock()
	defer hm.cacheLock.RUnlock()

	if status, exists := hm.healthCache[serviceKey]; exists {
		// Consider service stale if not checked recently
		staleDuration := hm.checkInterval * 3
		if time.Since(status.LastChecked) > staleDuration {
			status.IsHealthy = false
			status.ErrorMessage = "Service health check is stale"
			logger.LogDebug("Service marked stale due to inactivity", logrus.Fields{
				"service":        serviceKey,
				"last_checked":   status.LastChecked,
				"stale_duration": staleDuration,
			})
			status.LastChecked = time.Now() // Update last checked time
		}
		return status
	}

	return &Status{IsHealthy: false, ErrorMessage: "Service not registered"}
}

func (hm *Monitor) GetAllHealthStatus() map[string]*Status {
	if !hm.enabled {
		return make(map[string]*Status)
	}

	hm.cacheLock.RLock()
	defer hm.cacheLock.RUnlock()

	result := make(map[string]*Status)
	for key, status := range hm.healthCache {
		// Create a copy to avoid data races
		result[key] = &Status{
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

// monitorLoop runs the main health monitoring loop.
func (hm *Monitor) monitorLoop() {
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

// performHealthChecks checks health of all registered services.
func (hm *Monitor) performHealthChecks() {
	hm.cacheLock.RLock()
	servicesToCheck := make(map[string]int)

	// Get services from our own health cache that are actively monitored
	for key, health := range hm.healthCache {
		if health != nil {
			servicesToCheck[key] = health.Port
		}
	}
	hm.cacheLock.RUnlock()

	logger.LogDebug("Performing health checks", logrus.Fields{
		"service_count": len(servicesToCheck),
	})

	// Check each service in parallel
	var wg sync.WaitGroup
	for serviceKey, port := range servicesToCheck {
		wg.Add(1)
		go func(key string, p int) {
			defer wg.Done()
			hm.checkServiceHealth(key, p)
		}(serviceKey, port)
	}

	wg.Wait()
}

// checkServiceHealth performs a health check on a specific service.
func (hm *Monitor) checkServiceHealth(serviceKey string, port int) {
	startTime := time.Now()

	// Perform multiple health check types
	isHealthy, err := hm.performHealthCheck(port)
	responseTime := time.Since(startTime)

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	status, exists := hm.healthCache[serviceKey]
	if !exists {
		status = &Status{}
		hm.healthCache[serviceKey] = status
	}

	status.LastChecked = time.Now()
	status.ResponseTime = responseTime

	if isHealthy {
		hm.handleHealthyService(status, serviceKey, port, responseTime)
	} else {
		hm.handleUnhealthyService(status, serviceKey, port, err)
	}

	// Log periodic health status for debugging
	if status.FailureCount > 0 || responseTime > 100*time.Millisecond {
		logger.LogDebug("Health check result", logrus.Fields{
			"service":       serviceKey,
			"port":          port,
			"healthy":       isHealthy,
			"response_ms":   responseTime.Milliseconds(),
			"failure_count": status.FailureCount,
			"error":         status.ErrorMessage,
		})
	}
}

// handleHealthyService processes a healthy service check result.
func (hm *Monitor) handleHealthyService(
	status *Status,
	serviceKey string,
	port int,
	responseTime time.Duration,
) {
	if !status.IsHealthy {
		logger.LogDebug("Service recovered", logrus.Fields{
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
}

// handleUnhealthyService processes an unhealthy service check result.
func (hm *Monitor) handleUnhealthyService(
	status *Status,
	serviceKey string,
	port int,
	err error,
) {
	status.FailureCount++
	if err != nil {
		status.ErrorMessage = err.Error()
	}

	// Mark as unhealthy after max failures
	if status.FailureCount >= hm.maxFailures {
		if status.IsHealthy {
			logger.LogDebug("Service marked unhealthy", logrus.Fields{
				"service":       serviceKey,
				"port":          port,
				"failure_count": status.FailureCount,
				"error":         status.ErrorMessage,
			})
		}
		status.IsHealthy = false
	}
}

// performHealthCheck executes the actual health check.
func (hm *Monitor) performHealthCheck(port int) (bool, error) {
	// Try TCP connection first (fastest)
	if err := hm.checkTCPConnection(port); err != nil {
		return false, fmt.Errorf("TCP check failed: %w", err)
	}

	// Try HTTP request (more thorough)
	if err := hm.checkHTTPEndpoint(port); err != nil {
		// TCP works but HTTP doesn't - might be non-HTTP service
		logger.LogDebug("HTTP check failed, but TCP is working", logrus.Fields{
			"port":  port,
			"error": err.Error(),
		})
		// Consider it healthy if TCP works (might be gRPC, database, etc.)
		return true, nil //nolint:nilerr // Ignore HTTP error if TCP succeeded
	}

	return true, nil
}

// checkTCPConnection performs a simple TCP connection test.
func (hm *Monitor) checkTCPConnection(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), hm.timeout)
	if err != nil {
		return err
	}

	// Ensure conn.Close() error is checked
	if err := conn.Close(); err != nil {
		logger.LogError("Failed to close connection", err)
	}

	return nil
}

// checkHTTPEndpoint performs an HTTP health check.
func (hm *Monitor) checkHTTPEndpoint(port int) error {
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
		// Ensure resp.Body.Close() error is checked
		if err := resp.Body.Close(); err != nil {
			logger.LogError("Failed to close response body", err)
		}

		// Accept any HTTP response as healthy (even 404)
		if resp.StatusCode < 500 {
			return nil
		}
	}

	return errors.New("all HTTP endpoints returned 5xx errors")
}

// GetHealthMonitor returns the global health monitor instance.
func GetHealthMonitor() *Monitor {
	return globalHealthMonitor
}

// InitializeHealthMonitor initializes and starts the global health monitor.
func InitializeHealthMonitor(cfg config.Config) {
	if globalHealthMonitor == nil {
		globalHealthMonitor = NewHealthMonitor(cfg.Health)
		logger.LogDebug("Health monitor initialized", logrus.Fields{
			"enabled":      cfg.Health.Enabled,
			"interval":     cfg.Health.CheckInterval,
			"timeout":      cfg.Health.Timeout,
			"max_failures": cfg.Health.MaxFailures,
		})
	}
	if cfg.Health.Enabled && globalHealthMonitor != nil {
		globalHealthMonitor.Start()
		logger.LogDebug("Health monitor started successfully", logrus.Fields{})
	} else if !cfg.Health.Enabled {
		logger.LogDebug("Health monitor disabled by configuration", logrus.Fields{})
	}
}

// isBackendHealthy is a convenience function for the proxy.
func IsBackendHealthy(serviceKey string) bool {
	if globalHealthMonitor == nil {
		return true // Assume healthy if monitor not initialized
	}
	status := globalHealthMonitor.IsHealthy(serviceKey)
	return status.IsHealthy
}

// registerServiceForMonitoring registers a service when port-forward is created.
func RegisterServiceForMonitoring(serviceKey string, port int) {
	if globalHealthMonitor != nil {
		globalHealthMonitor.RegisterService(serviceKey, port)
	}
}
