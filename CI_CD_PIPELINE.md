# CI/CD Pipeline Documentation

This document describes the complete CI/CD pipeline setup for kube-tunnel using GoReleaser and GitHub Actions.

## 🚀 Pipeline Overview

The CI/CD pipeline is designed to provide:
- **Automated testing** on every push and PR
- **Multi-platform builds** for releases
- **Security scanning** and vulnerability detection
- **Automated releases** with GoReleaser
- **Multi-architecture Docker images**
- **Package manager distribution** (Homebrew, Scoop, Winget, Snap)
- **Performance benchmarking** and validation

## 📁 Pipeline Structure

```
.github/
├── workflows/
│   ├── ci.yml              # Continuous Integration
│   └── release.yml         # Release Pipeline
├── ISSUE_TEMPLATE/
│   ├── bug_report.yml      # Bug report template
│   └── feature_request.yml # Feature request template
├── pull_request_template.md
└── dependabot.yml          # Dependency updates

scripts/
├── perf-test.sh           # Performance testing
├── health-demo.sh         # Health monitoring demo
├── prepare-release.sh     # Release preparation
├── postinstall.sh         # Package post-install
└── preremove.sh           # Package pre-removal

.goreleaser.yaml           # GoReleaser configuration
Dockerfile                 # Multi-stage Docker build
CHANGELOG.md              # Release changelog
```

## 🔄 CI Pipeline (ci.yml)

### Triggers
- **Push** to `main` and `develop` branches
- **Pull requests** to `main` and `develop` branches

### Jobs Overview

#### 1. **Test Job**
```yaml
- Go version: 1.21
- Runs: go test -v -race -coverprofile=coverage.out ./...
- Uploads coverage to Codecov
- Matrix: ubuntu-latest
```

#### 2. **Lint Job**
```yaml
- Uses: golangci/golangci-lint-action@v3
- Timeout: 5 minutes
- Configuration: .golangci.yml
```

#### 3. **Build Job**
```yaml
Matrix:
  - linux/amd64, linux/arm64
  - darwin/amd64, darwin/arm64  
  - windows/amd64
- Cross-compilation with CGO_ENABLED=0
- Uploads build artifacts
```

#### 4. **Security Job**
```yaml
- Trivy vulnerability scanner
- SARIF report to GitHub Security tab
- Filesystem and dependency scanning
```

#### 5. **Docker Job**
```yaml
- Multi-platform Docker build
- BuildKit cache optimization
- No push (validation only)
```

#### 6. **Performance Job**
```yaml
- Builds binary and runs performance tests
- Installs hey for load testing
- Validates script functionality
```

#### 7. **Validate Job**
```yaml
- GoReleaser configuration check
- Dockerfile linting with Hadolint
- Configuration validation
```

#### 8. **Integration Job**
```yaml
- Kind Kubernetes cluster setup
- Real Kubernetes service testing
- End-to-end validation
```

## 🚢 Release Pipeline (release.yml)

### Triggers
- **Tags** matching `v*` pattern (e.g., v1.0.0)

### Required Secrets
```bash
GITHUB_TOKEN                    # Automatic (GitHub provides)
HOMEBREW_TAP_GITHUB_TOKEN      # For Homebrew tap updates
SCOOP_BUCKET_GITHUB_TOKEN      # For Scoop bucket updates  
WINGET_GITHUB_TOKEN            # For Winget package updates
DISCORD_WEBHOOK_URL            # For Discord notifications (optional)
```

### Release Process

#### 1. **Build & Release Job**
```yaml
- Multi-platform binary builds
- Docker image builds (amd64/arm64)
- Container signing with Cosign
- SBOM generation with Syft
- GitHub release creation
- Package manager distribution
```

#### 2. **Post-Release Job**
```yaml
- README badge updates
- Security scan reporting
- Package manager status updates
```

#### 3. **Verification Jobs**
```yaml
- Download and test binaries on multiple OS
- Docker image verification
- Multi-arch manifest inspection
```

#### 4. **Performance Benchmark**
```yaml
- Release binary performance testing
- Size and optimization verification
- Benchmark result reporting
```

## 📦 GoReleaser Configuration

### Build Targets
```yaml
Platforms:
  - linux/amd64, linux/arm64, linux/arm/v6, linux/arm/v7
  - darwin/amd64, darwin/arm64
  - windows/amd64

Features:
  - Cross-compilation with CGO disabled
  - Binary stripping (-s -w)
  - Version info injection
  - Trimmed build paths
```

### Distribution Channels

#### **GitHub Releases**
- Automated release creation
- Asset uploads with checksums
- Release notes generation
- Changelog integration

#### **Docker Images**
```yaml
Registry: ghcr.io/snakeice/kube-tunnel
Tags:
  - latest
  - v1.x.x
  - v1.x
  - v1

Multi-arch manifests:
  - linux/amd64
  - linux/arm64
```

#### **Package Managers**
```yaml
Homebrew:
  - Repository: snakeice/homebrew-tap
  - Formula generation
  - Automatic PR creation

Scoop (Windows):
  - Repository: snakeice/scoop-bucket
  - Manifest generation
  - JSON bucket updates

Winget (Windows):
  - Repository: microsoft/winget-pkgs
  - PR to official repository
  - Automated submission

Snapcraft:
  - Automatic store publication
  - Strict confinement
  - Multiple architectures

Linux Packages:
  - .deb (Debian/Ubuntu)
  - .rpm (RedHat/CentOS/Fedora)
  - .apk (Alpine)
  - .pkg.tar.xz (Arch Linux)
```

## 🐳 Docker Strategy

### Multi-Stage Build
```dockerfile
FROM golang:1.21-alpine AS builder
# Build optimizations and cross-compilation

FROM scratch AS final
# Minimal runtime with security hardening
```

### Image Features
- **Scratch-based** for minimal attack surface
- **Non-root user** (65534:65534)
- **Multi-architecture** support
- **Security labels** and metadata
- **Health checks** included
- **Cosign signing** for verification

### Image Variants
```bash
# Version-specific
ghcr.io/snakeice/kube-tunnel:v1.2.3
ghcr.io/snakeice/kube-tunnel:v1.2
ghcr.io/snakeice/kube-tunnel:v1

# Architecture-specific
ghcr.io/snakeice/kube-tunnel:latest-amd64
ghcr.io/snakeice/kube-tunnel:latest-arm64

# Latest
ghcr.io/snakeice/kube-tunnel:latest
```

## 🔒 Security Features

### Code Security
- **Trivy scanning** for vulnerabilities
- **Dependency checking** with Dependabot
- **SARIF reporting** to GitHub Security
- **Secret scanning** prevention

### Release Security
- **Cosign signing** for all artifacts
- **SBOM generation** for transparency
- **Checksum verification** for downloads
- **GPG signing** support for tags

### Container Security
- **Scratch base** images
- **Non-root execution**
- **Capability dropping**
- **Read-only filesystems**
- **Security labels** compliance

## 📊 Performance Monitoring

### Automated Benchmarks
```bash
Performance Targets:
- Cold start: < 200ms
- Warm requests: < 10ms  
- Throughput: > 1000 req/s
- Memory usage: < 50MB
```

### Performance Tests
- **Load testing** with hey
- **Health API** response times
- **Connection pooling** efficiency
- **Protocol negotiation** speed

## 🔧 Local Development

### Running CI Locally

#### **Using Act (GitHub Actions locally)**
```bash
# Install act
curl https://raw.githubusercontent.com/nektos/act/master/install.sh | sudo bash

# Run CI workflow
act push

# Run specific job
act -j test

# Run with secrets
act -s GITHUB_TOKEN=your_token
```

#### **Manual Testing**
```bash
# Run tests
go test -v -race ./...

# Run linting  
golangci-lint run

# Build cross-platform
GOOS=linux GOARCH=amd64 go build -o kube-tunnel-linux-amd64

# Test Docker build
docker build -t kube-tunnel:test .

# Validate GoReleaser
goreleaser check
```

### Performance Testing
```bash
# Run performance suite
./scripts/perf-test.sh

# Health monitoring demo
./scripts/health-demo.sh

# Manual performance test
hey -n 1000 -c 10 http://service.default.svc.cluster.local/
```

## 🚀 Release Process

### Automated Release (Recommended)
```bash
# 1. Prepare release
./scripts/prepare-release.sh patch

# 2. Push tag (triggers release)
git push origin v1.2.3

# 3. Monitor GitHub Actions
# https://github.com/snakeice/kube-tunnel/actions
```

### Manual Release Steps
```bash
# 1. Update version and changelog
vim CHANGELOG.md

# 2. Commit changes
git add .
git commit -m "chore: prepare release v1.2.3"

# 3. Create and push tag
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin main
git push origin v1.2.3

# 4. Wait for GitHub Actions to complete
```

### Release Verification
```bash
# Check release artifacts
curl -L https://github.com/snakeice/kube-tunnel/releases/latest

# Test Docker image
docker run --rm ghcr.io/snakeice/kube-tunnel:latest -help

# Verify signatures
cosign verify ghcr.io/snakeice/kube-tunnel:latest

# Test package managers
brew install snakeice/tap/kube-tunnel
```

## 📈 Monitoring & Observability

### GitHub Actions Monitoring
- **Workflow run times** and success rates
- **Build artifact sizes** and optimization
- **Test coverage** trends
- **Security scan** results

### Release Metrics
- **Download statistics** from GitHub Releases
- **Docker image pulls** from registry
- **Package manager** installation counts
- **Performance benchmark** results

### Alerts & Notifications
- **Failed builds** → GitHub notifications
- **Security vulnerabilities** → GitHub Security tab
- **Release completion** → Discord webhook (optional)
- **Dependency updates** → Dependabot PRs

## 🔍 Troubleshooting

### Common CI Issues

#### **Test Failures**
```bash
# Check test logs in GitHub Actions
# Run tests locally
go test -v ./...

# Run with race detection
go test -race ./...

# Check specific package
go test -v ./health_monitor
```

#### **Build Failures**
```bash
# Check cross-compilation
GOOS=linux GOARCH=arm64 go build .

# Verify dependencies
go mod tidy
go mod verify

# Check linting
golangci-lint run
```

#### **Docker Build Issues**
```bash
# Build locally
docker build --platform linux/amd64 -t test .

# Check multi-platform
docker buildx build --platform linux/amd64,linux/arm64 .

# Verify Dockerfile
hadolint Dockerfile
```

### Release Issues

#### **GoReleaser Failures**
```bash
# Validate configuration
goreleaser check

# Test release (dry run)
goreleaser release --snapshot --rm-dist

# Check specific section
goreleaser build --rm-dist
```

#### **Package Manager Issues**
```bash
# Check Homebrew formula
brew install --verbose snakeice/tap/kube-tunnel

# Test Scoop manifest
scoop install kube-tunnel

# Verify Winget package
winget install snakeice.kube-tunnel
```

## 📚 Additional Resources

### Documentation
- [GoReleaser Documentation](https://goreleaser.com/)
- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [Docker Multi-platform Builds](https://docs.docker.com/build/building/multi-platform/)
- [Cosign Documentation](https://docs.sigstore.dev/cosign/overview/)

### Tools
- [Act - Run GitHub Actions locally](https://github.com/nektos/act)
- [Hadolint - Dockerfile linter](https://github.com/hadolint/hadolint)
- [Trivy - Vulnerability scanner](https://github.com/aquasecurity/trivy)
- [Hey - HTTP load tester](https://github.com/rakyll/hey)

### Examples
- [Performance Testing](scripts/perf-test.sh)
- [Health Monitoring](scripts/health-demo.sh)  
- [Release Preparation](scripts/prepare-release.sh)
- [Docker Deployment](README.md#docker--kubernetes)

---

**Pipeline Status**: All systems operational ✅  
**Last Updated**: Auto-generated on release  
**Support**: [GitHub Issues](https://github.com/snakeice/kube-tunnel/issues)