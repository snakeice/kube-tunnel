# Kube-Tunnel Monitoring

This directory contains the monitoring configuration for kube-tunnel, including Prometheus metrics collection and Grafana dashboards.

## Overview

The monitoring system provides:
- **Prometheus metrics** exposed at `/metrics` endpoint
- **Grafana dashboard** for visualizing health metrics
- **Real-time monitoring** of service health status
- **Performance metrics** for health checks

## Metrics Available

### Health Check Metrics
- `kube_tunnel_health_checks_total` - Total number of health checks (success/failure)
- `kube_tunnel_health_check_duration_seconds` - Health check response time histogram
- `kube_tunnel_service_health_status` - Current health status per service (1=healthy, 0=unhealthy)
- `kube_tunnel_service_failure_count` - Consecutive failure count per service

### Service Registration Metrics
- `kube_tunnel_services_registered_total` - Total services being monitored
- `kube_tunnel_services_healthy_total` - Number of healthy services
- `kube_tunnel_services_unhealthy_total` - Number of unhealthy services

### Configuration Metrics
- `kube_tunnel_health_check_interval_seconds` - Health check interval
- `kube_tunnel_health_check_timeout_seconds` - Health check timeout
- `kube_tunnel_health_max_failures` - Maximum failures before marking unhealthy
- `kube_tunnel_health_monitor_enabled` - Whether monitoring is enabled

## Quick Start

### 1. Start the Monitoring Stack

```bash
cd monitoring
docker-compose up -d
```

This will start:
- Prometheus on http://localhost:9090
- Grafana on http://localhost:3000 (admin/admin)

### 2. Quick Real-time Dashboard (No Setup Required)

For instant real-time monitoring without Prometheus/Grafana:

```bash
cd monitoring
./serve-dashboard.sh
```

This starts a simple HTTP server and opens the dashboard at:
- **Real-time Dashboard**: http://localhost:8080/realtime-dashboard.html

**Features:**
- ✅ **No setup required** - works immediately
- ✅ **Real-time updates** every 5 seconds
- ✅ **Beautiful UI** with live status cards
- ✅ **Service health overview** with live updates
- ✅ **Response time metrics** and configuration
- ✅ **Mobile responsive** design

### 2. Configure Prometheus Data Source

1. Open Grafana at http://localhost:3000
2. Login with admin/admin
3. Go to Configuration → Data Sources
4. Add Prometheus data source:
   - URL: `http://prometheus:9090`
   - Access: Server (default)

### 3. Import the Dashboard

1. In Grafana, go to Dashboards → Import
2. Copy the contents of `grafana-dashboard.json`
3. Paste and import

## Local Development

### Running kube-tunnel with Metrics

```bash
# Build and run kube-tunnel
go build -o kube-tunnel cmd/main.go
./kube-tunnel

# The metrics endpoint will be available at:
# http://localhost:80/metrics
```

### Testing Metrics

```bash
# Check Prometheus metrics
curl http://localhost:80/metrics

# Check health status
curl http://localhost:80/health/status

# Check health metrics
curl http://localhost:80/health/metrics
```

## Production Deployment

### Kubernetes

For production Kubernetes deployments, you can:

1. **Use the provided Prometheus config** as a starting point
2. **Enable Kubernetes service discovery** by uncommenting the relevant section
3. **Configure persistent storage** for Prometheus and Grafana
4. **Set up alerting rules** based on the metrics

### Environment Variables

The monitoring system respects these environment variables:

```bash
# Health monitoring configuration
HEALTH_MONITOR_ENABLED=true
HEALTH_CHECK_INTERVAL=30s
HEALTH_CHECK_TIMEOUT=2s
HEALTH_MAX_FAILURES=3
```

## Dashboard Panels

### Grafana Dashboard

The Grafana dashboard includes:

1. **Service Health Status** - Real-time count of healthy/unhealthy services
2. **Response Time** - Average health check response time per service
3. **Success/Failure Rate** - Health check success and failure rates
4. **Failure Counts** - Consecutive failure counts per service
5. **Service Registration** - Total services being monitored
6. **Configuration** - Current health monitor settings

### Real-time HTML Dashboard

The HTML dashboard provides instant real-time monitoring:

1. **Status Cards** - Live monitor status, total services, healthy/unhealthy counts
2. **Service Health Overview** - Real-time list of all services with health status
3. **Response Times** - Live average, min, and max response times
4. **Health Check Volume** - Health ratios and configuration details
5. **Configuration Panel** - Current health monitor settings
6. **Auto-refresh** - Updates every 5 seconds automatically
7. **Visual Feedback** - Color-coded status indicators and animations

## Troubleshooting

### Common Issues

1. **Metrics not showing up**
   - Check if kube-tunnel is running and accessible
   - Verify the `/metrics` endpoint returns data
   - Check Prometheus targets page for scrape errors

2. **Dashboard not loading**
   - Ensure Prometheus data source is configured correctly
   - Check that metrics are being collected
   - Verify the dashboard JSON is valid

3. **High resource usage**
   - Adjust scrape intervals in Prometheus config
   - Consider reducing metric retention period
   - Monitor Prometheus and Grafana resource usage

### Debug Commands

```bash
# Check if metrics endpoint is working
curl -v http://localhost:80/metrics

# Check Prometheus targets
curl http://localhost:9090/api/v1/targets

# Check Prometheus metrics
curl http://localhost:9090/api/v1/query?query=up
```

## Contributing

To add new metrics:

1. **Define the metric** in `internal/metrics/metrics.go`
2. **Update the health monitor** to record the metric
3. **Add dashboard panels** to visualize the metric
4. **Update this documentation**

## References

- [Prometheus Documentation](https://prometheus.io/docs/)
- [Grafana Documentation](https://grafana.com/docs/)
- [Go Prometheus Client](https://github.com/prometheus/client_golang)
