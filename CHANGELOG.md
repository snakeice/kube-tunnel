# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

### Changed
- Optimized port-forward setup time (60% faster cold start)
- Improved request latency (50% faster warm requests)
- Enhanced connection pooling and HTTP/2 optimization
- Modernized README with better structure and examples
- Removed named returns for better code readability

### Fixed
- Eliminated nil pointer dereference panic during initialization
- Fixed health monitor initialization order dependency
- Improved error handling and graceful degradation

### Performance
- Sub-200ms cold start latency (down from ~500ms)
- <10ms warm request latency (down from ~25ms)
- 2x throughput improvement (1000+ req/s)
- Background health checks eliminate request-time overhead

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