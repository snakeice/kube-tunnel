# kube-tunnel

A modern Kubernetes service proxy with intelligent protocol detection, structured logging, and automatic DNS resolution for `*.svc.cluster.local` domains.

A lightweight HTTP proxy server that automatically creates Kubernetes port-forwards to services using DNS-style routing, with built-in zeroconf/mDNS support for seamless service discovery and standards-compliant service registration.

## Overview

kube-tunnel acts as a smart proxy that intercepts HTTP requests formatted as Kubernetes service DNS names and automatically establishes port-forwards to the appropriate pods. This enables seamless access to Kubernetes services without manually managing port-forwards.

## Features

- **Multi-Protocol Support**: HTTP/1.1, HTTP/2 (h2c), HTTP/2 over TLS (h2), and gRPC
- **Automatic Port-Forwarding**: Dynamically creates port-forwards to Kubernetes services
- **DNS-Style Routing**: Uses familiar Kubernetes DNS format (`service.namespace.svc.cluster.local`)
- **Zeroconf Service Discovery**: Built-in zeroconf/mDNS server with full service discovery and automatic DNS resolution for `*.svc.cluster.local` (using libp2p/zeroconf/v2)
- **Intelligent Caching**: Reuses existing port-forwards and automatically expires idle connections
- **Auto-Discovery**: Automatically finds running pods for services using label selectors
- **Protocol Negotiation**: Automatic HTTP/2 upgrade and fallback support
- **gRPC Support**: Optimized handling for gRPC services over HTTP/2
- **TLS Support**: Self-signed certificate generation for HTTPS/h2 testing
- **Comprehensive Logging**: Detailed logging with timing metrics and structured fields
- **Connection Management**: Automatic cleanup of idle port-forwards with configurable timeouts
- **Smart Retry Logic**: Exponential backoff with context cancellation support
- **Cross-Platform**: Works on macOS, Linux, and Windows with automatic DNS configuration
- **Standards-Compliant**: RFC 6762 (mDNS) and RFC 6763 (DNS-SD) compliant service discovery
- **Service Registration**: Automatic registration and discovery of Kubernetes services
- **In-Cluster & Local Support**: Works both inside Kubernetes clusters and with local kubeconfig

## How It Works

1. **Request Interception**: The proxy receives HTTP requests with hosts like `service.namespace.svc.cluster.local`
2. **Service Discovery**: Looks up the service and finds a running pod using Kubernetes API
3. **Port-Forward Creation**: Establishes a port-forward from a local port to the pod
4. **Request Proxying**: Forwards the HTTP request to the local port-forward
5. **Caching**: Maintains the port-forward for future requests to the same service
6. **Auto-Cleanup**: Automatically closes idle port-forwards after 10 minutes

## Installation

### Prerequisites

- Go 1.24.5 or later
- Access to a Kubernetes cluster
- Valid kubeconfig file (for local usage) or in-cluster service account

### Build from Source

```bash
git clone https://github.com/snakeice/kube-tunnel
cd kube-tunnel
go mod download
go build -o kube-tunnel .
```

## Protocol Support

kube-tunnel supports ALL HTTP protocols on a single port for maximum compatibility:

### Supported Protocols

| Protocol | Port | Description | Use Case |
|----------|------|-------------|----------|
| HTTP/1.1 | 80 | Standard HTTP cleartext | Legacy applications, simple requests |
| HTTP/1.1 TLS | 80 | Standard HTTP over TLS | Secure legacy applications |
| h2c | 80 | HTTP/2 cleartext | Modern apps without TLS overhead |
| h2 (TLS) | 80 | HTTP/2 over TLS | Secure connections, production use |
| gRPC | 80 | gRPC over HTTP/2 | Microservices, high-performance APIs |

### Single Port Architecture

- **Port 80**: ALL protocols with automatic detection
  - Protocol detection at connection level
  - TLS vs cleartext automatic routing
  - HTTP/2 vs HTTP/1.1 negotiation
  - gRPC optimized handling

## Usage

### Running the Proxy

```bash
# Run the proxy server with full DNS automation
./kube-tunnel

# Run on a different port
./kube-tunnel -port=8080

# Run without automatic DNS configuration
./kube-tunnel -no-dns

# Run without mDNS server
./kube-tunnel -no-mdns

# Configure DNS only (don't start proxy)
./kube-tunnel -dns-only

# Clean up DNS configuration
./kube-tunnel -cleanup
```

The server starts on port 80 (configurable) and automatically handles all protocols:
- Port 80: ALL protocols (HTTP/1.1, h2c, h2 with TLS, gRPC)
- Automatic protocol detection and routing
- Built-in mDNS server for `*.svc.cluster.local` resolution
- System DNS configuration (macOS/Linux)
- No manual DNS configuration needed - just works!

### Making Requests

With automatic DNS configuration, you can directly use Kubernetes service DNS names:

```bash
# Direct requests - DNS automatically resolves to proxy
curl http://temporal.temporal.svc.cluster.local/health
curl http://prometheus.monitoring.svc.cluster.local:9090/metrics
grpcurl temporal.temporal.svc.cluster.local:80 list
```

Or send HTTP requests using the traditional proxy approach:

#### HTTP/1.1 Requests (Port 80)
```bash
# Standard HTTP/1.1 request
curl --http1.1 http://my-service.default.svc.cluster.local/api/health

# POST request with JSON
curl -X POST --http1.1 http://api-service.staging.svc.cluster.local/data \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}'
```

#### HTTP/2 Cleartext (h2c) Requests (Port 80)
```bash
# Force HTTP/2 cleartext
curl --http2-prior-knowledge http://my-service.default.svc.cluster.local/api/health

# HTTP/2 with automatic upgrade attempt
curl --http2 http://my-service.default.svc.cluster.local/api/health
```

#### HTTP/2 over TLS (h2) Requests (Port 80)
```bash
# HTTPS with HTTP/2 on same port
curl --http2 --insecure https://my-service.default.svc.cluster.local/api/health

# Verify TLS certificate (in production)
curl --http2 --cacert server.crt https://my-service.default.svc.cluster.local/api/health
```

#### gRPC-style Requests (Port 80)
```bash
# gRPC request over h2c
curl --http2-prior-knowledge \
  -H "Content-Type: application/grpc+proto" \
  -H "grpc-encoding: gzip" \
  -H "TE: trailers" \
  -X POST \
  http://grpc-service.default.svc.cluster.local/my.package.Service/Method

# gRPC request over h2 (TLS) - same port!
curl --http2 --insecure \
  -H "Content-Type: application/grpc+proto" \
  -H "grpc-encoding: gzip" \
  -H "TE: trailers" \
  -X POST \
  https://grpc-service.default.svc.cluster.local/my.package.Service/Method
```

### Health Check

Test protocol support using the built-in health endpoint:

```bash
# Check health with different protocols - all on port 80!
curl http://localhost/health                           # HTTP/1.1 on port 80
curl --http2-prior-knowledge http://localhost/health   # h2c on port 80
curl --http2 --insecure https://localhost/health       # h2 on port 80
```

### Configuration

The proxy automatically detects the Kubernetes configuration:

1. **In-Cluster**: Uses service account credentials when running inside a pod
2. **Local Development**: Uses `~/.kube/config` when running locally

#### Environment Variables

- `HOME`: Used to locate kubeconfig file (`$HOME/.kube/config`)
- `LOG_LEVEL`: Set logging level (error, warn, info, debug)
- `PROXY_MAX_RETRIES`: Maximum retry attempts (0-10, default: 3)
- `PROXY_RETRY_DELAY_MS`: Base retry delay in milliseconds (50-5000, default: 200)
- Kubernetes environment variables (when running in-cluster)

#### Protocol Configuration

The server automatically:
- Generates self-signed certificates for TLS testing
- Configures optimal HTTP/2 settings for gRPC
- Enables CORS headers for browser compatibility
- Handles protocol negotiation and fallback

### Docker Usage

```dockerfile
FROM golang:1.24.5-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o kube-tunnel .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/kube-tunnel .
EXPOSE 80
CMD ["./kube-tunnel"]
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-tunnel
  namespace: default
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
          image: your-registry/kube-tunnel:latest
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-tunnel
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-tunnel
rules:
  - apiGroups: [""]
    resources: ["services", "pods"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods/portforward"]
    verbs: ["create"]
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
---
apiVersion: v1
kind: Service
metadata:
  name: kube-tunnel
  namespace: default
spec:
  selector:
    app: kube-tunnel
  ports:
    - port: 80
      targetPort: 80
  type: ClusterIP
```

## Architecture

### Components

- **`main.go`**: Entry point and HTTP server setup
- **`proxy.go`**: HTTP request handler and proxy logic
- **`cache.go`**: Port-forward session management and caching
- **`k8s.go`**: Kubernetes API interactions and service discovery
- **`portforward.go`**: Port-forward establishment and management
- **`util.go`**: Utility functions for host parsing and port allocation

### Flow Diagram

```text
HTTP Request → Proxy Handler → Host Parser → Service Discovery → Port-Forward Cache → Pod Port-Forward → Response
```

## Testing Protocol Support

Use the included test script to verify all protocols work correctly:

```bash
# Make the script executable
chmod +x test-protocols.sh

# Run protocol tests
./test-protocols.sh
```

The test script validates:
- HTTP/1.1 cleartext on port 80
- HTTP/1.1 over TLS on port 80
- h2c (HTTP/2 cleartext) on port 80
- h2 (HTTP/2 over TLS) on port 80
- gRPC-like requests over HTTP/2 on port 80
- Automatic protocol detection and routing

## Configuration Options

### Default Settings

- **Single Port**: 80 (ALL protocols: HTTP/1.1, h2c, h2 with TLS, gRPC)
- **Target Port**: 8080 (configurable in code)
- **Idle Timeout**: 10 minutes
- **Cache Check Interval**: 1 minute
- **HTTP/2 Settings**: Optimized for gRPC (1000 streams, 1MB frames)

### DNS Configuration

kube-tunnel automatically configures your system to resolve `*.svc.cluster.local` domains to the proxy server.

#### Automatic DNS Setup

```bash
# Setup DNS automatically (included in normal startup)
./kube-tunnel

# Setup DNS only, don't start proxy
./kube-tunnel -dns-only

# Check DNS configuration
./setup-dns.sh status

# Clean up DNS configuration
./kube-tunnel -cleanup
```

#### Platform Support

| Platform | Method | Location | Automatic |
|----------|---------|----------|-----------|
| **macOS** | Resolver | `/etc/resolver/cluster.local` | ✅ Yes |
| **Linux** | Hosts file | `/etc/hosts` | ✅ Yes |
| **Windows** | Manual | `C:\Windows\System32\drivers\etc\hosts` | ⚠️  Manual |

#### mDNS Server

The built-in mDNS server responds to DNS queries for `*.svc.cluster.local` domains:

- **Address**: `224.0.0.251:5353` (standard mDNS multicast)
- **Supported Records**: A, AAAA, SRV, TXT
- **TTL**: 60 seconds
- **Automatic**: Works with most modern operating systems

```bash
# Test mDNS resolution
dig @224.0.0.251 -p 5353 temporal.temporal.svc.cluster.local

# Disable mDNS server
./kube-tunnel -no-mdns
```

#### Manual Configuration

If automatic setup fails, configure DNS manually:

**macOS:**
```bash
sudo mkdir -p /etc/resolver
sudo tee /etc/resolver/cluster.local << EOF
nameserver 127.0.0.1
port 5353
EOF
```

**Linux:**
```bash
echo "127.0.0.1 temporal.temporal.svc.cluster.local" | sudo tee -a /etc/hosts
echo "127.0.0.1 prometheus.monitoring.svc.cluster.local" | sudo tee -a /etc/hosts
```

**Windows:**
```cmd
# Add to C:\Windows\System32\drivers\etc\hosts
127.0.0.1 temporal.temporal.svc.cluster.local
127.0.0.1 prometheus.monitoring.svc.cluster.local
```

### Retry Configuration

The proxy includes intelligent retry logic for connection failures with configurable settings:

#### Environment Variables

```bash
# Maximum number of retry attempts (0-10, default: 3)
export PROXY_MAX_RETRIES=3

# Base delay between retries in milliseconds (50-5000ms, default: 200)
export PROXY_RETRY_DELAY_MS=200
```

#### Retry Behavior

- **Exponential Backoff**: Delays increase as 200ms → 400ms → 800ms → 1600ms (capped at 5s)
- **Retryable Errors**: Connection refused, timeout, network unreachable, connection reset
- **Non-Retryable Errors**: HTTP errors, protocol mismatches, authentication failures
- **Backend Health Checks**: Validates port-forward readiness before proxying
- **Connection Validation**: Tests TCP connectivity before declaring port-forward ready
- **Context Cancellation**: Stops retries immediately when request is canceled

#### Example Configurations

```bash
# Conservative (slower but more reliable)
export PROXY_MAX_RETRIES=5
export PROXY_RETRY_DELAY_MS=500

# Aggressive (faster but less resilient)
export PROXY_MAX_RETRIES=1
export PROXY_RETRY_DELAY_MS=100

# Disabled (no retries)
export PROXY_MAX_RETRIES=0
```

The retry mechanism is particularly useful for:
- Temporary network connectivity issues
- Port-forwards that take time to establish
- Backend services that are slow to start
- Kubernetes API server delays
- gRPC services with protocol detection

## Logging

The application provides comprehensive logging for all operations:

- Request handling and routing
- Service discovery and pod selection
- Port-forward creation and management
- Cache operations and cleanup
- Error conditions and debugging information

## Troubleshooting

### Common Issues

1. **DNS Resolution Failures**

   ```text
   curl: (6) Could not resolve host: temporal.temporal.svc.cluster.local
   ```

   **Solutions:**
   - Run `./setup-dns.sh test` to diagnose DNS issues
   - Check if kube-tunnel is running: `./setup-dns.sh status`
   - Manually configure DNS: `./setup-dns.sh manual`
   - Try without DNS: `curl -H "Host: temporal.temporal.svc.cluster.local" http://localhost/health`

2. **Service Not Found**

   ```text
   Failed to get service namespace/service: services "service" not found
   ```

   **Solutions:**
   - Verify the service exists: `kubectl get svc -n namespace`
   - Check RBAC permissions: `kubectl auth can-i get services`
   - Ensure correct namespace in domain name

3. **No Running Pods**

   ```text
   No running pod for service/namespace
   ```

   **Solutions:**
   - Check pod status: `kubectl get pods -n namespace`
   - Verify service selector: `kubectl describe svc -n namespace service-name`
   - Ensure pods are in Running state

4. **Port-Forward Failures**

   ```text
   Port-forward failed for namespace/pod: error forwarding port
   ```

   **Solutions:**
   - Verify network connectivity to the cluster
   - Check if the target port is available: `kubectl get pods -o wide`
   - Try manual port-forward: `kubectl port-forward pod-name local:remote`
   - Check for firewall/network policy issues

5. **Protocol Issues**

   ```text
   HTTP/2 not working or gRPC requests failing
   ```

   **Solutions:**
   - Enable debug logging: `LOG_LEVEL=debug ./kube-tunnel`
   - Try protocol fallback: requests automatically try h2c then HTTP/1.1
   - For gRPC: Ensure proper headers: `Content-Type: application/grpc+proto`
   - Check service compatibility: `curl -v http://service.namespace.svc.cluster.local/`

6. **Retry Exhaustion**

   ```text
   Connection failed after all retries
   ```

   **Solutions:**
   - Increase retry attempts: `export PROXY_MAX_RETRIES=5`
   - Increase retry delay: `export PROXY_RETRY_DELAY_MS=500`
   - Check backend service health
   - Verify Kubernetes cluster connectivity

7. **mDNS Issues**

   ```text
   mDNS server failed to start
   ```

   **Solutions:**
   - Run with elevated privileges (may be required for mDNS)
   - Disable mDNS: `./kube-tunnel -no-mdns`
   - Check for port 5353 conflicts: `lsof -i :5353`
   - Use manual DNS configuration instead

8. **Permission Errors**

   ```text
   No write permission to /etc/hosts or /etc/resolver
   ```

   **Solutions:**
   - Run with sudo for DNS setup: `sudo ./kube-tunnel -dns-only`
   - Use manual configuration without elevated privileges
   - Disable automatic DNS: `./kube-tunnel -no-dns`

### Debug Mode

Increase logging verbosity to troubleshoot issues:

```bash
# Enable debug logging
LOG_LEVEL=debug ./kube-tunnel

# Test DNS setup with detailed output
./setup-dns.sh test

# Check current status
./setup-dns.sh status
```

Debug logs include:
- DNS resolution and mDNS queries
- Protocol detection and fallback
- Retry attempts with timing
- Port-forward establishment
- Request/response metrics
- Connection health checks
- gRPC-specific headers and status codes

### Protocol Testing

Test different protocols and DNS resolution:

```bash
# Test with automatic DNS resolution
curl -v http://temporal.temporal.svc.cluster.local/health

# Test protocol detection
curl -v --http2-prior-knowledge http://temporal.temporal.svc.cluster.local/health

# Test gRPC
grpcurl -plaintext temporal.temporal.svc.cluster.local:80 list

# Test manual host header (bypass DNS)
curl -v -H "Host: temporal.temporal.svc.cluster.local" http://localhost/health

# Test mDNS directly
dig @224.0.0.251 -p 5353 temporal.temporal.svc.cluster.local

# Run comprehensive tests
./setup-dns.sh test
```

### Testing Tools

Use the included testing scripts:

```bash
# Setup and test DNS
./setup-dns.sh setup
./setup-dns.sh test

# Test retry functionality
./test-retry.sh

# Test gRPC specifically
./test-grpc-retry.sh

# Test different log levels
LOG_LEVEL=debug ./demo-logging.sh
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request
