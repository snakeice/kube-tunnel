# Configuration Priority

kube-tunnel uses a layered configuration system with the following priority (from highest to lowest):

1. **Command-line flags**
   - Example: `--port 8080`, `--verbose`, etc.
   - These take precedence over all other settings

2. **Environment variables**
   - All prefixed with `KTUN_`
   - Example: `KTUN_DNS_IP=10.0.0.1`, `KTUN_USE_VIRTUAL=true`
   - These override config file settings

3. **Config file (YAML)**
   - Specified with `--config` flag
   - Example: `./kube-tunnel --config config.yaml`
   - Default search locations (if no file specified):
     - `./config.yaml` (current directory)
     - `$HOME/.kube-tunnel/config.yaml` (user's home directory)
     - `/etc/kube-tunnel/config.yaml` (system-wide)

4. **Default values**
   - Hard-coded defaults used when no other config is provided

## Configuration File Format

The configuration file uses YAML format with the following structure:

```yaml
# Network settings
network:
  useVirtualInterface: true
  virtualInterfaceName: "kube-dns0"
  virtualInterfaceIP: "10.8.0.1"
  portForwardInterfaceName: "kube-proxy0"
  portForwardInterfaceIP: "10.8.0.2"
  dnsBindIP: "10.8.0.1"
  portForwardBindIP: "10.8.0.2"
  customIPRanges:
    - "10.8.0.0/24"
    - "10.9.0.0/24"

# Performance tuning
performance:
  skipHealthCheck: false
  forceHTTP2: true
  disableProtocolFallback: false
  maxIdleConns: 200
  maxIdleConnsPerHost: 50
  maxConnsPerHost: 100
  readTimeout: "120s"
  writeTimeout: "120s"
  idleTimeout: "120s"
  responseHeaderTimeout: "30s"
  proxyTimeout: "60s"
  maxConcurrentStreams: 1000
  maxFrameSize: 524288
  grpcTimeout: "30s"
  maxUploadBufferPerConnection: 1048576
  maxUploadBufferPerStream: 1048576

# Health monitoring
health:
  enabled: true
  checkInterval: "30s"
  timeout: "2s"
  maxFailures: 3

# Proxy settings
proxy:
  maxRetries: 2
  retryDelay: "100ms"
```

See `config.example.yaml` for a complete example with documentation.
