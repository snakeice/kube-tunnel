# kube-tunnel

> A high-performance Kubernetes service proxy with intelligent protocol detection and automatic service discovery.

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24.5-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()

**kube-tunnel** transforms how you access Kubernetes services by providing a smart proxy that automatically creates port-forwards using DNS-style routing. No more manual `kubectl port-forward` commands—just use standard HTTP clients with Kubernetes service names.

## ✨ Features

- 🚀 **Multi-Protocol Support** - HTTP/1.1, HTTP/2 (h2c/h2), and gRPC on a single port
- 🔗 **Automatic Port-Forwarding** - Dynamic port-forwards with intelligent caching
- 🌐 **DNS Integration** - Built-in mDNS server with automatic system DNS configuration
- 📊 **Health Monitoring** - Background health checks with real-time APIs
- ⚡ **High Performance** - Sub-200ms cold start, <10ms warm requests
- 🔄 **Smart Retry Logic** - Exponential backoff with configurable policies
- 🖥️ **Cross-Platform** - macOS, Linux, and Windows support
- ☁️ **Cloud Native** - Works in-cluster and locally with kubeconfig

## 🚀 Quick Start

### Installation

```bash
git clone https://github.com/snakeice/kube-tunnel
cd kube-tunnel
go build -o kube-tunnel .
```

### Basic Usage

```bash
# Start the proxy (auto-configures DNS)
./kube-tunnel

# Make requests using Kubernetes DNS names
curl http://my-service.default.svc.cluster.local/api
curl http://prometheus.monitoring.svc.cluster.local:9090/metrics
grpcurl temporal.temporal.svc.cluster.local:80 list
```

That's it! No manual port-forwarding required.

## 📖 How It Works

```mermaid
graph LR
    A[HTTP Request] --> B[DNS Resolution]
    B --> C[kube-tunnel Proxy]
    C --> D[Service Discovery]
    D --> E[Pod Selection]
    E --> F[Port-Forward]
    F --> G[Response]
```

1. **DNS Resolution**: `*.svc.cluster.local` domains resolve to the proxy
2. **Service Discovery**: Proxy finds the target service and pods
3. **Port-Forward**: Creates a cached port-forward to a healthy pod
4. **Request Proxying**: Forwards request with automatic protocol detection
5. **Response**: Returns the response with proper headers and status

## 🎯 Protocol Support

All protocols work on **port 80** with automatic detection:

| Protocol | Example | Use Case |
|----------|---------|----------|
| **HTTP/1.1** | `curl http://api.default.svc.cluster.local/` | Legacy apps, simple requests |
| **HTTP/2** | `curl --http2-prior-knowledge http://api.default.svc.cluster.local/` | Modern apps, multiplexing |
| **gRPC** | `grpcurl api.default.svc.cluster.local:80 list` | Microservices, streaming |
| **HTTPS** | `curl -k https://api.default.svc.cluster.local/` | Secure connections |

## ⚙️ Configuration

### Command Line Options

```bash
./kube-tunnel [options]

Options:
  -port int        Port to run proxy on (default: 80)
  -no-mdns        Disable mDNS server
  -no-dns         Skip automatic DNS configuration
  -dns-only       Setup DNS only, don't start proxy
  -cleanup        Remove DNS configuration and exit
  -help           Show help message
```

### Environment Variables

#### Performance Tuning
```bash
# Health monitoring
export HEALTH_MONITOR_ENABLED=true
export HEALTH_CHECK_INTERVAL=30s
export HEALTH_CHECK_TIMEOUT=2s

# Connection optimization
export MAX_IDLE_CONNS=300
export MAX_IDLE_CONNS_PER_HOST=100
export FORCE_HTTP2=true

# Retry behavior
export PROXY_MAX_RETRIES=2
export PROXY_RETRY_DELAY_MS=100
```

#### Quick Performance Modes
```bash
# Speed mode (development)
export SKIP_HEALTH_CHECK=true
export PROXY_MAX_RETRIES=1
export FORCE_HTTP2=true

# Production mode (reliability)
export HEALTH_MONITOR_ENABLED=true
export MAX_IDLE_CONNS=500
export MAX_CONCURRENT_STREAMS=3000
```

## 📊 Health Monitoring

Built-in health monitoring eliminates request-time health checks:

### API Endpoints

```bash
# Basic proxy health
curl http://localhost:80/health

# Detailed service health status
curl http://localhost:80/health/status | jq

# Health metrics and statistics
curl http://localhost:80/health/metrics | jq

# Active services
curl http://localhost:80/services | jq
```

### Example Health Response

```json
{
  "status": "ok",
  "monitor_enabled": true,
  "total_services": 3,
  "services": [
    {
      "service": "api.default",
      "healthy": true,
      "last_checked": "2024-01-15T10:30:00Z",
      "response_time": 25,
      "failure_count": 0
    }
  ]
}
```

## 🌐 DNS Configuration

### Automatic Setup

kube-tunnel automatically configures DNS resolution:

| Platform | Method | Status |
|----------|--------|--------|
| **macOS** | `/etc/resolver/cluster.local` | ✅ Automatic |
| **Linux** | `/etc/hosts` entries | ✅ Automatic |
| **Windows** | Manual configuration | ⚠️ Manual |

### Manual Configuration

If automatic setup fails:

**macOS:**
```bash
sudo mkdir -p /etc/resolver
echo -e "nameserver 127.0.0.1\nport 5353" | sudo tee /etc/resolver/cluster.local
```

**Linux:**
```bash
echo "127.0.0.1 *.svc.cluster.local" | sudo tee -a /etc/hosts
```

## 🐳 Docker & Kubernetes

### Docker

```dockerfile
FROM golang:1.24.5-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o kube-tunnel .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/kube-tunnel /usr/local/bin/
EXPOSE 80
CMD ["kube-tunnel"]
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-tunnel
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-tunnel
  template:
    metadata:
      labels:
        app: kube-tunnel
    spec:
      serviceAccountName: kube-tunnel
      containers:
      - name: kube-tunnel
        image: kube-tunnel:latest
        ports:
        - containerPort: 80
        env:
        - name: HEALTH_MONITOR_ENABLED
          value: "true"
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-tunnel
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-tunnel
rules:
- apiGroups: [""]
  resources: ["services", "pods", "pods/portforward"]
  verbs: ["get", "list", "create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-tunnel
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-tunnel
subjects:
- kind: ServiceAccount
  name: kube-tunnel
  namespace: default
```

## 🧪 Testing & Performance

### Performance Test

```bash
# Run comprehensive performance test
./scripts/perf-test.sh

# Health monitoring demo
./scripts/health-demo.sh

# Manual performance check
time curl http://my-service.default.svc.cluster.local/health
```

### Load Testing

```bash
# HTTP load test
hey -n 1000 -c 10 http://api.default.svc.cluster.local/

# gRPC load test
ghz --insecure -n 1000 -c 10 api.default.svc.cluster.local:80
```

### Expected Performance

| Metric | Target | Optimized |
|--------|--------|-----------|
| Cold start latency | <500ms | <200ms |
| Warm request latency | <25ms | <10ms |
| Throughput | >200 req/s | >1000 req/s |
| Health API latency | <50ms | <10ms |

## 🔧 Troubleshooting

### Common Issues

<details>
<summary><strong>DNS Resolution Fails</strong></summary>

```bash
# Check DNS configuration
./kube-tunnel -dns-only

# Test manually
curl -H "Host: service.namespace.svc.cluster.local" http://localhost:80/

# Check mDNS
dig @127.0.0.1 -p 5353 service.namespace.svc.cluster.local
```
</details>

<details>
<summary><strong>Service Not Found</strong></summary>

```bash
# Verify service exists
kubectl get svc -n namespace

# Check permissions
kubectl auth can-i get services
kubectl auth can-i create pods/portforward

# Enable debug logging
LOG_LEVEL=debug ./kube-tunnel
```
</details>

<details>
<summary><strong>Slow Performance</strong></summary>

```bash
# Enable performance mode
export SKIP_HEALTH_CHECK=true
export PROXY_MAX_RETRIES=1
export FORCE_HTTP2=true

# Check health status
curl http://localhost:80/health/metrics

# Run performance test
./scripts/perf-test.sh
```
</details>

### Debug Mode

```bash
# Enable debug logging
export LOG_LEVEL=debug
./kube-tunnel

# Monitor health in real-time
watch 'curl -s http://localhost:80/health/metrics | jq ".total_services, .healthy_services"'
```

## 🤝 Contributing

We welcome contributions! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Development Setup

```bash
# Clone and build
git clone https://github.com/snakeice/kube-tunnel
cd kube-tunnel
go mod download
go build .

# Performance testing
./scripts/perf-test.sh
```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## ⭐ Show Your Support

If kube-tunnel helps you, please give it a ⭐ on GitHub! It helps others discover the project.

---

**Made with ❤️ for the Kubernetes community**
