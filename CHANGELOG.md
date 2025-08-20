# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Free Local IP Management**: Automatic allocation of unused local IP addresses (127.0.0.2+) for port forwards to avoid localhost conflicts
- New environment variables for IP configuration:
  - `USE_FREE_LOCAL_IP`: Enable/disable automatic IP allocation (default: true)
  - `FORCE_LOCAL_IP`: Force a specific local IP address
  - `PROXY_BIND_IP`: Configure proxy server bind address
  - `DNS_BIND_IP`: Configure DNS server bind address  
  - `PORT_FORWARD_BIND_IP`: Configure port forward bind address
- IP Manager for intelligent local IP allocation and lifecycle management
- Enhanced DNS server to automatically resolve to allocated IPs
- Test suite for validating Free Local IP functionality (`examples/test-free-local-ip.sh`)
- Configuration examples (`examples/free-local-ip.env`)
- Comprehensive documentation (`FREE_LOCAL_IP.md`)

### Changed
- Port forwards now use allocated local IPs instead of hardcoded 127.0.0.1
- DNS resolution automatically points to the correct allocated IP
- Cache interface updated to return both IP and port information
- Kubernetes port forwarding enhanced to support specific IP addresses

### Fixed
- Eliminated localhost conflicts when running local services alongside kube-tunnel
- Improved support for running multiple kube-tunnel instances simultaneously
- Better network isolation between local and Kubernetes services

### Added (since last update)

- Background health monitoring system for continuous service health tracking
- Performance optimization with configurable HTTP transport settings
- Health status APIs (`/health/status` and `/health/metrics`)
- Environment variable configuration for performance tuning
- Performance testing suite with automated benchmarking
- GoReleaser configuration for automated releases
- Multi-platform Docker images with security scanning
- Package manager support (Homebrew, Scoop, Winget, Snap)
- Comprehensive CI/CD pipeline with GitHub Actions
- Security scanning with Trivy and Cosign signing
- Lightweight dependency injection container (`internal/app/container.go`) for explicit wiring
- Struct-based `Proxy` handler with method-scoped health endpoints
- Cache now registers/unregisters services with the health monitor automatically
- JSON responses unified via `encoding/json` encoder across health endpoints
- Network interface name validation (regex) in DNS resolver with documented `#nosec` justification

### Changed

- Optimized port-forward setup time (60% faster cold start)
- Improved request latency (50% faster warm requests)
- Enhanced connection pooling and HTTP/2 optimization
- Modernized README with better structure and examples
- Removed named returns for better code readability
- **DNS resolver code translated from Portuguese to English for better maintainability**
- **DNS resolver now uses structured logging with the logger package**
- Replaced remaining global singletons (proxy, health monitor accessors, port-forward cache) with injected dependencies
- Port-forward setup & validation logic refactored for clearer error paths and structured logging
- Health handlers migrated from package-level functions to methods on `Proxy` (clean architecture alignment)
- Cache constructor now accepts a `*health.Monitor` enabling lifecycle integration
- Reduced long lines & improved formatting (golines compliance)

### Fixed

- Eliminated nil pointer dereference panic during initialization
- Fixed health monitor initialization order dependency
- Improved error handling and graceful degradation
- **Enhanced DNS error messages with clearer English descriptions**
- Removed stray legacy code paths causing potential nil monitor lookups during refactor
- Eliminated syntax issues introduced during refactor (cache file reconstruction)

### Removed

- Global health monitor accessor functions (`GetHealthMonitor`, etc.)
- Legacy package-level proxy handler (`legacyHandler`) and health HTTP handlers
- Global port-forward cache singleton in favor of injected cache instance

### Security (hardening)

- Added strict interface name validation to DNS revert/setup to mitigate command injection vectors

### Deprecated

- Package-level proxy and health handler functions (now fully superseded by `Proxy` methods); will be removed entirely in next minor release if no external usage surfaces

### Performance

- Sub-200ms cold start latency (down from ~500ms)
- <10ms warm request latency (down from ~25ms)
- 2x throughput improvement (1000+ req/s)
- Background health checks eliminate request-time overhead

### Developer Experience

- **Improved DNS resolver code readability with English comments and messages**
- **Better debugging experience with structured DNS logging**
- **Consistent error messages across the DNS module**

## [1.0.0] - TBD

### Added

- Initial release of kube-tunnel
- Multi-protocol support (HTTP/1.1, HTTP/2, gRPC)
- Automatic port-forwarding with intelligent caching
- DNS-style routing using Kubernetes service names
- Built-in mDNS server for automatic DNS resolution
- Cross-platform support (macOS, Linux, Windows)
- Service discovery and pod selection
- Automatic cleanup of idle port-forwards
- Comprehensive logging with structured fields
- Smart retry logic with exponential backoff
- TLS support with self-signed certificate generation
- Command-line interface with flexible options

### Security

- Minimal attack surface with scratch-based Docker images
- Non-root user execution in containers
- Capability dropping and security constraints
- SBOM generation for all release artifacts
- Cosign signing for container images and binaries

### Documentation

- Complete README with quick start guide
- Performance optimization guides
- Health monitoring documentation
- Docker and Kubernetes deployment examples
- Troubleshooting guide with common issues
- Contributing guidelines for developers

---

## Version History

- **v1.x.x** - Production-ready releases with performance optimizations
- **v0.x.x** - Development and preview releases
- **Unreleased** - Latest development changes

## Migration Guides

### From 0.x to 1.0

- Update Docker image references to use new registry
- Review environment variable changes for health monitoring
- Update Kubernetes RBAC permissions if needed
- Test performance improvements in your environment

## Support

- 📚 [Documentation](https://github.com/snakeice/kube-tunnel/blob/main/README.md)
- 🐛 [Issue Tracker](https://github.com/snakeice/kube-tunnel/issues)
- 💬 [Discussions](https://github.com/snakeice/kube-tunnel/discussions)
- 📈 [Performance Guide](https://github.com/snakeice/kube-tunnel/blob/main/PERFORMANCE.md)
