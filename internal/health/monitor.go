package health

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/metrics"
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
	// Track whether response time comes from real requests or health checks
	ResponseTimeSource string // "real_request" or "health_check"
}

// Monitor manages background health checks for all active services.

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

// Global state removed: instances are now created and owned by the application container.

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

	// Update configuration metrics
	metrics.UpdateHealthConfig(
		hm.checkInterval.Seconds(),
		hm.timeout.Seconds(),
		hm.maxFailures,
		hm.enabled,
	)

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
			IsHealthy:          true, // Assume healthy initially
			LastChecked:        time.Now(),
			LastHealthy:        time.Now(),
			FailureCount:       0,
			ResponseTime:       0,
			Port:               port,
			ResponseTimeSource: "health_check", // Initial health check
		}

		// Update metrics for new service
		metrics.UpdateHealthStatus(serviceKey, strconv.Itoa(port), true, 0)
		hm.updateServiceCountMetrics()

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

	if status, exists := hm.healthCache[serviceKey]; exists {
		// Remove metrics for this service
		metrics.UnregisterService(serviceKey, strconv.Itoa(status.Port))

		delete(hm.healthCache, serviceKey)
		hm.updateServiceCountMetrics()

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

	// Record metrics for this health check
	status := "success"
	if !isHealthy {
		status = "failure"
	}
	metrics.RecordHealthCheck(serviceKey, status, responseTime.Seconds())

	// Log response time collection for debugging
	if responseTime > 0 {
		logger.LogDebug("Health check response time recorded", logrus.Fields{
			"service":     serviceKey,
			"port":        port,
			"response_ms": responseTime.Milliseconds(),
			"status":      status,
		})
	}

	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	statusObj, exists := hm.healthCache[serviceKey]
	if !exists {
		statusObj = &Status{}
		hm.healthCache[serviceKey] = statusObj
	}

	statusObj.LastChecked = time.Now()

	// Only record response time for successful health checks
	// Failed checks (connection refused, etc.) should not contribute to response time metrics
	if isHealthy {
		statusObj.ResponseTime = responseTime
		statusObj.ResponseTimeSource = "health_check"
	} else if statusObj.ResponseTime == 0 {
		// For failed checks, keep the last known good response time
		// This prevents 0ms from failed checks from skewing the metrics
		statusObj.ResponseTime = 0 // Keep as 0 if we never had a successful check
		// Don't update ResponseTime for failed checks
	}

	if isHealthy {
		hm.handleHealthyService(statusObj, serviceKey, port, responseTime)
	} else {
		hm.handleUnhealthyService(statusObj, serviceKey, port, err)
	}

	// Update metrics for this service
	metrics.UpdateHealthStatus(
		serviceKey,
		strconv.Itoa(port),
		statusObj.IsHealthy,
		statusObj.FailureCount,
	)

	// Log periodic health status for debugging
	if statusObj.FailureCount > 0 || responseTime > 100*time.Millisecond {
		logger.LogDebug("Health check result", logrus.Fields{
			"service":       serviceKey,
			"port":          port,
			"healthy":       isHealthy,
			"response_ms":   responseTime.Milliseconds(),
			"failure_count": statusObj.FailureCount,
			"error":         statusObj.ErrorMessage,
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

	// Auto-cleanup services that have been consistently failing for too long
	// This prevents zombie services from cluttering the metrics
	maxFailureThreshold := hm.maxFailures * 3 // Allow 3x the normal failure threshold
	if status.FailureCount >= maxFailureThreshold {
		logger.LogDebug("Auto-removing consistently failing service", logrus.Fields{
			"service":       serviceKey,
			"port":          port,
			"failure_count": status.FailureCount,
			"max_threshold": maxFailureThreshold,
		})

		// Schedule removal in the next iteration to avoid deadlock
		go func() {
			time.Sleep(100 * time.Millisecond) // Small delay to avoid race conditions
			hm.UnregisterService(serviceKey)
		}()
	}
}

// performHealthCheck executes the actual health check.
func (hm *Monitor) performHealthCheck(port int) (bool, error) {
	// Try TCP connection first (most reliable indicator of service availability)
	if err := hm.checkTCPConnection(port); err != nil {
		return false, fmt.Errorf("TCP check failed: %w", err)
	}

	// If TCP works, the service is considered healthy
	// We don't require HTTP to work since many services (databases, gRPC, etc.) don't serve HTTP
	// The fact that TCP accepts connections means the service is running and listening
	return true, nil
}

// checkTCPConnection performs a simple TCP connection test.
func (hm *Monitor) checkTCPConnection(port int) error {
	// Use a shorter timeout for health checks to avoid blocking
	timeout := min(hm.timeout, 2*time.Second)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), timeout)
	if err != nil {
		return fmt.Errorf("connection refused or timeout: %w", err)
	}

	// Ensure conn.Close() error is checked
	if err := conn.Close(); err != nil {
		logger.LogError("Failed to close connection", err)
	}

	return nil
}

// updateServiceCountMetrics updates the service count metrics.
func (hm *Monitor) updateServiceCountMetrics() {
	total := len(hm.healthCache)
	healthy := 0
	unhealthy := 0

	for _, status := range hm.healthCache {
		if status.IsHealthy {
			healthy++
		} else {
			unhealthy++
		}
	}

	metrics.UpdateServiceCounts(total, healthy, unhealthy)
}

// UpdateServicePerformance updates the service performance data from real proxy requests
// This provides actual user experience metrics rather than synthetic health checks.
func (hm *Monitor) UpdateServicePerformance(
	serviceKey string,
	responseTime time.Duration,
	success bool,
) {
	hm.cacheLock.Lock()
	defer hm.cacheLock.Unlock()

	statusObj, exists := hm.healthCache[serviceKey]
	if !exists {
		statusObj = hm.createNewServiceEntry(responseTime)
		hm.healthCache[serviceKey] = statusObj
	} else {
		hm.updateExistingServiceEntry(statusObj, responseTime, success)
	}

	// Update metrics for this service
	if statusObj.Port > 0 {
		metrics.UpdateHealthStatus(
			serviceKey,
			strconv.Itoa(statusObj.Port),
			statusObj.IsHealthy,
			statusObj.FailureCount,
		)
	}

	// Update service count metrics
	hm.updateServiceCountMetrics()
}

// createNewServiceEntry creates a new service entry for performance monitoring.
func (hm *Monitor) createNewServiceEntry(responseTime time.Duration) *Status {
	return &Status{
		IsHealthy:          true, // Assume healthy initially
		LastChecked:        time.Now(),
		LastHealthy:        time.Now(),
		FailureCount:       0,
		ResponseTime:       responseTime,
		Port:               0, // Will be set when port-forward is established
		ResponseTimeSource: "real_request",
	}
}

// updateExistingServiceEntry updates an existing service entry with new performance data.
func (hm *Monitor) updateExistingServiceEntry(
	statusObj *Status,
	responseTime time.Duration,
	success bool,
) {
	statusObj.LastChecked = time.Now()
	statusObj.ResponseTime = responseTime
	statusObj.ResponseTimeSource = "real_request"

	if success {
		statusObj.IsHealthy = true
		statusObj.LastHealthy = time.Now()
		statusObj.FailureCount = 0
		statusObj.ErrorMessage = ""
	} else {
		statusObj.FailureCount++
		if statusObj.FailureCount >= hm.maxFailures {
			statusObj.IsHealthy = false
			statusObj.ErrorMessage = fmt.Sprintf("Request failed (HTTP %d)", statusObj.FailureCount)
		}
	}
}

// Removed: global accessor helpers; use Monitor instance directly.
