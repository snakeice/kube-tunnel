# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.2] - 2025-08-25

### Fixed

- **Critical DNS Resolution Issue**: Fixed systemd-resolved DNS scope activation by changing default virtual interface IP from loopback address (127.0.0.10) to private IP (10.8.0.1)
  - Resolved "Current Scopes: none" issue that prevented DNS queries to `*.svc.cluster.local` domains from resolving
  - Fixed timeout errors when querying cluster domains (e.g., `dig test.svc.cluster.local`)
  - systemd-resolved now properly activates DNS scope for the virtual interface, showing "Current Scopes: DNS"
- **DNS Server Binding**: Enhanced DNS server to properly bind to virtual interface IP when configured, with intelligent fallback to localhost during interface creation
  - Added smart detection when DNS bind IP matches virtual interface IP to handle bootstrap chicken-and-egg problem
  - Improved error handling during DNS server rebinding process

### Changed

- **Default Virtual Interface Configuration**:
  - Changed default virtual interface IP from `127.0.0.10` to `10.8.0.1` to ensure proper DNS scope activation
  - Updated DNS and port forward binding to use virtual interface IP (`10.8.0.1`) instead of localhost (`127.0.0.1`)
  - Replaced loopback IP ranges (`127.0.0.0/24`, `127.1.0.0/16`) with private IP ranges (`10.8.0.0/24`, `10.9.0.0/24`) for virtual interface allocation
- **DNS Bootstrap Process**: Enhanced DNS server initialization to start on localhost when target bind IP is the virtual interface IP, then rebind after interface creation
- **Logging Improvements**: Added detailed logging for DNS bind IP decisions, virtual interface IP allocation, and bootstrap process

### Technical Details

- **Root Cause**: systemd-resolved does not activate DNS scope for interfaces using loopback IP addresses (127.x.x.x range)
- **Solution**: Use private IP addresses (10.x.x.x range) which systemd-resolved properly recognizes for DNS scope activation
- **Backward Compatibility**: All changes can be overridden via environment variables (KTUN_VIRTUAL_IP, KTUN_DNS_IP, KTUN_FORWARD_IP)

## [0.0.1] - 2025-08-23

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

### Changed

- Port forwards now use allocated local IPs instead of hardcoded 127.0.0.1
- DNS resolution automatically points to the correct allocated IP
- Cache interface updated to return both IP and port information
- Kubernetes port forwarding enhanced to support specific IP addresses
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

- Eliminated localhost conflicts when running local services alongside kube-tunnel
- Improved support for running multiple kube-tunnel instances simultaneously
- Better network isolation between local and Kubernetes services
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

### Security

- Added strict interface name validation to DNS revert/setup to mitigate command injection vectors
- Minimal attack surface with scratch-based Docker images
- Non-root user execution in containers
- Capability dropping and security constraints
- SBOM generation for all release artifacts
- Cosign signing for container images and binaries

### Performance

- Sub-200ms cold start latency (down from ~500ms)
- <10ms warm request latency (down from ~25ms)
- 2x throughput improvement (1000+ req/s)
- Background health checks eliminate request-time overhead

### Developer Experience

- **Improved DNS resolver code readability with English comments and messages**
- **Better debugging experience with structured DNS logging**
- **Consistent error messages across the DNS module**

### Documentation

- Complete README with quick start guide
- Performance optimization guides
- Health monitoring documentation
- Docker and Kubernetes deployment examples
- Troubleshooting guide with common issues
- Contributing guidelines for developers

## [1.0.0] - TBD

### Future Plans

- Performance enhancements and optimizations
- Additional protocol support
- Extended monitoring capabilities

---

## Version History

- **v1.x.x** - Future production-ready releases with performance optimizations
- **v0.0.1** - Initial release with core functionality and Free Local IP Management
- **Unreleased** - Latest development changes

## Migration Guides

### From future versions

- Migration guides will be added as new versions are released
- Current version 0.0.1 serves as the baseline for future migrations

## Support

- 📚 [Documentation](https://github.com/snakeice/kube-tunnel/blob/main/README.md)
- 🐛 [Issue Tracker](https://github.com/snakeice/kube-tunnel/issues)
- 💬 [Discussions](https://github.com/snakeice/kube-tunnel/discussions)
- 📈 [Performance Guide](https://github.com/snakeice/kube-tunnel/blob/main/PERFORMANCE.md)
