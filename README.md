# kube-tunnel

A lightweight HTTP proxy server that automatically creates Kubernetes port-forwards to services using DNS-style routing.

## Overview

kube-tunnel acts as a smart proxy that intercepts HTTP requests formatted as Kubernetes service DNS names and automatically establishes port-forwards to the appropriate pods. This enables seamless access to Kubernetes services without manually managing port-forwards.

## Features

- **Automatic Port-Forwarding**: Dynamically creates port-forwards to Kubernetes services
- **DNS-Style Routing**: Uses familiar Kubernetes DNS format (`service.namespace.svc.cluster.local`)
- **Intelligent Caching**: Reuses existing port-forwards and automatically expires idle connections
- **Auto-Discovery**: Automatically finds running pods for services using label selectors
- **Comprehensive Logging**: Detailed logging for debugging and monitoring
- **Connection Management**: Automatic cleanup of idle port-forwards after 10 minutes
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

## Usage

### Running the Proxy

```bash
# Run the proxy server
./kube-tunnel
```

The server starts on port 80 and begins accepting requests.

### Making Requests

Send HTTP requests using the Kubernetes service DNS format:

```bash
# Access a service in the default namespace
curl http://my-service.default.svc.cluster.local/api/health

# Access a service in a specific namespace
curl http://web-app.production.svc.cluster.local/status

# Any HTTP method is supported
curl -X POST http://api-service.staging.svc.cluster.local/data \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}'
```

### Configuration

The proxy automatically detects the Kubernetes configuration:

1. **In-Cluster**: Uses service account credentials when running inside a pod
2. **Local Development**: Uses `~/.kube/config` when running locally

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

## Configuration Options

### Environment Variables

- `HOME`: Used to locate kubeconfig file (`$HOME/.kube/config`)
- Kubernetes environment variables (when running in-cluster)

### Default Settings

- **Listen Port**: 80
- **Target Port**: 8080 (configurable in code)
- **Idle Timeout**: 10 minutes
- **Cache Check Interval**: 1 minute

## Logging

The application provides comprehensive logging for all operations:

- Request handling and routing
- Service discovery and pod selection
- Port-forward creation and management
- Cache operations and cleanup
- Error conditions and debugging information

## Troubleshooting

### Common Issues

1. **Service Not Found**

   ```text
   Failed to get service namespace/service: services "service" not found
   ```

   - Verify the service exists in the specified namespace
   - Check RBAC permissions

2. **No Running Pods**

   ```text
   No running pod for service/namespace
   ```

   - Ensure the service has running pods
   - Check pod status and readiness

3. **Port-Forward Failures**

   ```text
   Port-forward failed for namespace/pod: error forwarding port
   ```

   - Verify network connectivity to the cluster
   - Check if the target port is available on the pod

### Debug Mode

Increase logging verbosity by examining the detailed log output. All operations are logged with context.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request
