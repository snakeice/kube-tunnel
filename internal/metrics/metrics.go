package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Health check metrics.
	HealthCheckTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kube_tunnel_health_checks_total",
			Help: "Total number of health checks performed",
		},
		[]string{"service", "status"},
	)

	HealthCheckDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_tunnel_health_check_duration_seconds",
			Help:    "Duration of health checks in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service"},
	)

	HealthStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_service_health_status",
			Help: "Current health status of monitored services (1 = healthy, 0 = unhealthy)",
		},
		[]string{"service", "port"},
	)

	HealthFailureCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_service_failures",
			Help: "Number of consecutive failures for each service",
		},
		[]string{"service", "port"},
	)

	// Service registration metrics.
	ServicesRegistered = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_services_registered",
			Help: "Number of services currently registered for health monitoring",
		},
	)

	ServicesHealthy = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_services_healthy",
			Help: "Number of services currently healthy",
		},
	)

	ServicesUnhealthy = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_services_unhealthy",
			Help: "Number of services currently unhealthy",
		},
	)

	// Health monitor configuration metrics.
	HealthCheckInterval = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_health_check_interval_seconds",
			Help: "Current health check interval in seconds",
		},
	)

	HealthCheckTimeout = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_health_check_timeout_seconds",
			Help: "Current health check timeout in seconds",
		},
	)

	HealthMaxFailures = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_health_max_failures",
			Help: "Maximum number of failures before marking service as unhealthy",
		},
	)

	// System metrics.
	HealthMonitorEnabled = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_tunnel_health_monitor_enabled",
			Help: "Whether the health monitor is currently enabled (1 = enabled, 0 = disabled)",
		},
	)

	// Real request response time metrics.
	RequestResponseTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_tunnel_request_response_time_seconds",
			Help:    "Response time of real proxy requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "namespace", "method", "status_code"},
	)

	RequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kube_tunnel_requests_total",
			Help: "Total number of proxy requests",
		},
		[]string{"service", "namespace", "method", "status_code"},
	)

	RequestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_tunnel_request_size_bytes",
			Help:    "Size of proxy requests in bytes",
			Buckets: prometheus.ExponentialBuckets(64, 2, 20), // 64B to ~64MB
		},
		[]string{"service", "namespace"},
	)

	ResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_tunnel_response_size_bytes",
			Help:    "Size of proxy responses in bytes",
			Buckets: prometheus.ExponentialBuckets(64, 2, 20), // 64B to ~64MB
		},
		[]string{"service", "namespace", "status_code"},
	)
)

// RecordHealthCheck records a health check result.
func RecordHealthCheck(service string, status string, duration float64) {
	HealthCheckTotal.WithLabelValues(service, status).Inc()
	HealthCheckDuration.WithLabelValues(service).Observe(duration)
}

// UpdateHealthStatus updates the current health status of a service.
func UpdateHealthStatus(service string, port string, isHealthy bool, failureCount int) {
	status := 0.0
	if isHealthy {
		status = 1.0
	}
	HealthStatus.WithLabelValues(service, port).Set(status)
	HealthFailureCount.WithLabelValues(service, port).Set(float64(failureCount))
}

// UpdateServiceCounts updates the total service counts.
func UpdateServiceCounts(total, healthy, unhealthy int) {
	ServicesRegistered.Set(float64(total))
	ServicesHealthy.Set(float64(healthy))
	ServicesUnhealthy.Set(float64(unhealthy))
}

// UpdateHealthConfig updates the health monitor configuration metrics.
func UpdateHealthConfig(checkInterval, timeout float64, maxFailures int, enabled bool) {
	HealthCheckInterval.Set(checkInterval)
	HealthCheckTimeout.Set(timeout)
	HealthMaxFailures.Set(float64(maxFailures))

	enabledValue := 0.0
	if enabled {
		enabledValue = 1.0
	}
	HealthMonitorEnabled.Set(enabledValue)
}

// UnregisterService removes metrics for a specific service.
func UnregisterService(service string, port string) {
	HealthStatus.DeleteLabelValues(service, port)
	HealthFailureCount.DeleteLabelValues(service, port)
}

// RecordRequestMetrics records metrics for real proxy requests.
func RecordRequestMetrics(
	service, namespace, method string,
	statusCode int,
	responseTime time.Duration,
	requestSize, responseSize int64,
) {
	statusCodeStr := strconv.Itoa(statusCode)

	RequestResponseTime.WithLabelValues(service, namespace, method, statusCodeStr).
		Observe(responseTime.Seconds())
	RequestTotal.WithLabelValues(service, namespace, method, statusCodeStr).Inc()

	if requestSize > 0 {
		RequestSize.WithLabelValues(service, namespace).Observe(float64(requestSize))
	}

	if responseSize > 0 {
		ResponseSize.WithLabelValues(service, namespace, statusCodeStr).
			Observe(float64(responseSize))
	}
}
