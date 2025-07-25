#!/bin/bash

set -e

# Release preparation script for kube-tunnel
# This script prepares everything needed for a new release

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CURRENT_VERSION=""
NEW_VERSION=""
RELEASE_TYPE=""
DRY_RUN=false
SKIP_TESTS=false
SKIP_BUILD=false

# Help function
show_help() {
    cat << EOF
🚀 kube-tunnel Release Preparation Script

USAGE:
    $0 [OPTIONS] <release-type>

RELEASE TYPES:
    major       Bump major version (1.0.0 -> 2.0.0)
    minor       Bump minor version (1.0.0 -> 1.1.0)
    patch       Bump patch version (1.0.0 -> 1.0.1)
    <version>   Set specific version (e.g., 1.2.3)

OPTIONS:
    -d, --dry-run           Show what would be done without making changes
    -h, --help              Show this help message
    --skip-tests            Skip running tests
    --skip-build            Skip building binaries
    --current <version>     Specify current version (auto-detected if not provided)

EXAMPLES:
    $0 patch                    # Bump patch version
    $0 minor                    # Bump minor version
    $0 1.2.3                    # Set specific version
    $0 --dry-run patch          # Show what would happen
    $0 --skip-tests minor       # Skip tests, bump minor version

ENVIRONMENT VARIABLES:
    GITHUB_TOKEN               Required for GitHub operations
    GPG_KEY_ID                 Optional: GPG key for signing
    SKIP_DOCKER               Set to skip Docker image preparation

REQUIREMENTS:
    - git (with clean working directory)
    - go (for building and testing)
    - github cli (gh) for GitHub operations
    - docker (optional, for container builds)
    - cosign (optional, for signing)

EOF
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -d|--dry-run)
                DRY_RUN=true
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            --skip-tests)
                SKIP_TESTS=true
                shift
                ;;
            --skip-build)
                SKIP_BUILD=true
                shift
                ;;
            --current)
                CURRENT_VERSION="$2"
                shift 2
                ;;
            major|minor|patch)
                RELEASE_TYPE="$1"
                shift
                ;;
            v*)
                NEW_VERSION="$1"
                shift
                ;;
            [0-9]*)
                NEW_VERSION="v$1"
                shift
                ;;
            *)
                echo -e "${RED}❌ Unknown option: $1${NC}"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done

    if [[ -z "$RELEASE_TYPE" && -z "$NEW_VERSION" ]]; then
        echo -e "${RED}❌ Please specify a release type or version${NC}"
        echo "Use --help for usage information"
        exit 1
    fi
}

# Check prerequisites
check_prerequisites() {
    echo -e "${BLUE}🔍 Checking prerequisites...${NC}"

    # Check if we're in a git repository
    if ! git rev-parse --git-dir > /dev/null 2>&1; then
        echo -e "${RED}❌ Not in a git repository${NC}"
        exit 1
    fi

    # Check for clean working directory
    if [[ -n $(git status --porcelain) ]]; then
        echo -e "${RED}❌ Working directory is not clean${NC}"
        echo "Please commit or stash your changes before preparing a release"
        exit 1
    fi

    # Check for required tools
    local required_tools=("git" "go")
    local optional_tools=("gh" "docker" "cosign")

    for tool in "${required_tools[@]}"; do
        if ! command -v "$tool" &> /dev/null; then
            echo -e "${RED}❌ Required tool not found: $tool${NC}"
            exit 1
        fi
    done

    for tool in "${optional_tools[@]}"; do
        if ! command -v "$tool" &> /dev/null; then
            echo -e "${YELLOW}⚠️  Optional tool not found: $tool${NC}"
        fi
    done

    # Check GitHub token
    if [[ -z "$GITHUB_TOKEN" ]] && command -v gh &> /dev/null; then
        if ! gh auth status &> /dev/null; then
            echo -e "${YELLOW}⚠️  GitHub CLI not authenticated${NC}"
            echo "Set GITHUB_TOKEN or run 'gh auth login'"
        fi
    fi

    echo -e "${GREEN}✅ Prerequisites check completed${NC}"
}

# Get current version from git tags
get_current_version() {
    if [[ -n "$CURRENT_VERSION" ]]; then
        echo "$CURRENT_VERSION"
        return
    fi

    local latest_tag
    latest_tag=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    echo "$latest_tag"
}

# Calculate next version
calculate_next_version() {
    local current="$1"
    local type="$2"

    # Remove 'v' prefix for calculation
    current="${current#v}"

    # Split version into parts
    local major minor patch
    IFS='.' read -r major minor patch <<< "$current"

    case "$type" in
        major)
            echo "v$((major + 1)).0.0"
            ;;
        minor)
            echo "v${major}.$((minor + 1)).0"
            ;;
        patch)
            echo "v${major}.${minor}.$((patch + 1))"
            ;;
        *)
            echo -e "${RED}❌ Invalid release type: $type${NC}"
            exit 1
            ;;
    esac
}

# Run tests
run_tests() {
    if [[ "$SKIP_TESTS" == "true" ]]; then
        echo -e "${YELLOW}⏭️  Skipping tests${NC}"
        return
    fi

    echo -e "${BLUE}🧪 Running tests...${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would run: go test -v -race ./...${NC}"
        return
    fi

    cd "$REPO_ROOT"

    # Run Go tests
    go test -v -race ./... || {
        echo -e "${RED}❌ Tests failed${NC}"
        exit 1
    }

    # Run linting
    if command -v golangci-lint &> /dev/null; then
        golangci-lint run || {
            echo -e "${RED}❌ Linting failed${NC}"
            exit 1
        }
    fi

    echo -e "${GREEN}✅ Tests passed${NC}"
}

# Build binaries
build_binaries() {
    if [[ "$SKIP_BUILD" == "true" ]]; then
        echo -e "${YELLOW}⏭️  Skipping build${NC}"
        return
    fi

    echo -e "${BLUE}🔨 Building binaries...${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would build binaries for multiple platforms${NC}"
        return
    fi

    cd "$REPO_ROOT"

    # Build for multiple platforms
    local platforms=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64")

    for platform in "${platforms[@]}"; do
        local goos="${platform%/*}"
        local goarch="${platform#*/}"
        local output="kube-tunnel-${goos}-${goarch}"

        if [[ "$goos" == "windows" ]]; then
            output="${output}.exe"
        fi

        echo "Building for ${goos}/${goarch}..."

        GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
            -ldflags="-w -s -X main.version=${NEW_VERSION} -X main.commit=$(git rev-parse HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
            -o "dist/${output}" . || {
            echo -e "${RED}❌ Build failed for ${goos}/${goarch}${NC}"
            exit 1
        }
    done

    echo -e "${GREEN}✅ Binaries built successfully${NC}"
}

# Update version in files
update_version_files() {
    echo -e "${BLUE}📝 Updating version in files...${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would update version to ${NEW_VERSION} in:${NC}"
        echo -e "${CYAN}  - README.md${NC}"
        echo -e "${CYAN}  - CHANGELOG.md${NC}"
        echo -e "${CYAN}  - .goreleaser.yaml${NC}"
        return
    fi

    cd "$REPO_ROOT"

    # Update README badges
    if [[ -f "README.md" ]]; then
        # Update version badge
        sed -i.bak "s/version-[^-]*-blue/version-${NEW_VERSION#v}-blue/g" README.md
        rm -f README.md.bak
    fi

    # Update CHANGELOG.md
    if [[ -f "CHANGELOG.md" ]]; then
        # Add new version entry at the top
        local changelog_entry="## [${NEW_VERSION#v}] - $(date +%Y-%m-%d)

### Added
- Release ${NEW_VERSION}

"
        # Insert after the "## [Unreleased]" section
        awk -v entry="$changelog_entry" '
        /^## \[Unreleased\]/ {
            print $0
            print ""
            print entry
            next
        }
        {print}
        ' CHANGELOG.md > CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md
    fi

    echo -e "${GREEN}✅ Version files updated${NC}"
}

# Create git tag
create_git_tag() {
    echo -e "${BLUE}🏷️  Creating git tag...${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would create tag: ${NEW_VERSION}${NC}"
        return
    fi

    cd "$REPO_ROOT"

    # Commit version changes
    if [[ -n $(git status --porcelain) ]]; then
        git add .
        git commit -m "chore: prepare release ${NEW_VERSION}"
    fi

    # Create annotated tag
    local tag_message="Release ${NEW_VERSION}

Auto-generated release tag.

View the changelog at: https://github.com/snakeice/kube-tunnel/blob/main/CHANGELOG.md"

    if [[ -n "$GPG_KEY_ID" ]]; then
        git tag -s "$NEW_VERSION" -m "$tag_message"
        echo -e "${GREEN}✅ Signed tag created: ${NEW_VERSION}${NC}"
    else
        git tag -a "$NEW_VERSION" -m "$tag_message"
        echo -e "${GREEN}✅ Tag created: ${NEW_VERSION}${NC}"
    fi
}

# Validate GoReleaser config
validate_goreleaser() {
    echo -e "${BLUE}🔍 Validating GoReleaser configuration...${NC}"

    if ! command -v goreleaser &> /dev/null; then
        echo -e "${YELLOW}⚠️  GoReleaser not found, skipping validation${NC}"
        return
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would validate .goreleaser.yaml${NC}"
        return
    fi

    cd "$REPO_ROOT"

    goreleaser check || {
        echo -e "${RED}❌ GoReleaser configuration is invalid${NC}"
        exit 1
    }

    echo -e "${GREEN}✅ GoReleaser configuration is valid${NC}"
}

# Generate release notes
generate_release_notes() {
    echo -e "${BLUE}📄 Generating release notes...${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}[DRY RUN] Would generate release notes${NC}"
        return
    fi

    cd "$REPO_ROOT"

    local release_notes_file="release-notes-${NEW_VERSION}.md"
    local previous_version
    previous_version=$(get_current_version)

    cat > "$release_notes_file" << EOF
# kube-tunnel ${NEW_VERSION}

## 🚀 What's New

Auto-generated release notes for ${NEW_VERSION}.

## 📊 Performance Highlights
- ⚡ Sub-200ms cold start latency
- 🚀 <10ms warm request latency
- 📈 1000+ req/s throughput
- 🔍 Background health monitoring

## 🛠️ Installation

### Binary Release
\`\`\`bash
# Linux (x86_64)
curl -L https://github.com/snakeice/kube-tunnel/releases/download/${NEW_VERSION}/kube-tunnel_Linux_x86_64.tar.gz | tar xz

# macOS (x86_64)
curl -L https://github.com/snakeice/kube-tunnel/releases/download/${NEW_VERSION}/kube-tunnel_Darwin_x86_64.tar.gz | tar xz

# Windows (x86_64)
curl -L https://github.com/snakeice/kube-tunnel/releases/download/${NEW_VERSION}/kube-tunnel_Windows_x86_64.zip -o kube-tunnel.zip
\`\`\`

### Docker
\`\`\`bash
docker pull ghcr.io/snakeice/kube-tunnel:${NEW_VERSION}
docker pull ghcr.io/snakeice/kube-tunnel:latest
\`\`\`

### Package Managers
\`\`\`bash
# Homebrew
brew install snakeice/tap/kube-tunnel

# Scoop (Windows)
scoop bucket add snakeice https://github.com/snakeice/scoop-bucket
scoop install kube-tunnel
\`\`\`

## 🔄 Upgrade Guide

No breaking changes in this release. Simply replace your existing binary or update your container image.

## 📚 Documentation

- [Quick Start Guide](https://github.com/snakeice/kube-tunnel#-quick-start)
- [Performance Guide](https://github.com/snakeice/kube-tunnel/blob/main/PERFORMANCE.md)
- [Health Monitoring](https://github.com/snakeice/kube-tunnel/blob/main/HEALTH_OPTIMIZATION.md)

## 🐛 Bug Reports

Found an issue? Please report it at: https://github.com/snakeice/kube-tunnel/issues

## 📈 Full Changelog

https://github.com/snakeice/kube-tunnel/compare/${previous_version}...${NEW_VERSION}
EOF

    echo -e "${GREEN}✅ Release notes generated: ${release_notes_file}${NC}"
}

# Main release preparation function
prepare_release() {
    echo -e "${GREEN}🚀 Preparing kube-tunnel release${NC}"
    echo -e "${BLUE}===========================================${NC}"

    # Get current version
    local current_version
    current_version=$(get_current_version)

    # Calculate new version
    if [[ -z "$NEW_VERSION" ]]; then
        NEW_VERSION=$(calculate_next_version "$current_version" "$RELEASE_TYPE")
    fi

    echo -e "${CYAN}Current version: ${current_version}${NC}"
    echo -e "${CYAN}New version: ${NEW_VERSION}${NC}"
    echo -e "${CYAN}Dry run: ${DRY_RUN}${NC}"
    echo ""

    # Confirmation
    if [[ "$DRY_RUN" != "true" ]]; then
        echo -e "${YELLOW}⚠️  This will create a new release: ${NEW_VERSION}${NC}"
        echo -e "${YELLOW}Are you sure you want to continue? [y/N]${NC}"
        read -r response
        if [[ "$response" != "y" && "$response" != "Y" ]]; then
            echo -e "${YELLOW}❌ Release preparation cancelled${NC}"
            exit 0
        fi
    fi

    # Run preparation steps
    check_prerequisites
    run_tests
    build_binaries
    update_version_files
    validate_goreleaser
    generate_release_notes
    create_git_tag

    echo ""
    echo -e "${GREEN}🎉 Release preparation completed!${NC}"
    echo -e "${BLUE}===========================================${NC}"

    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${CYAN}This was a dry run. No changes were made.${NC}"
    else
        echo -e "${GREEN}✅ Release ${NEW_VERSION} is ready!${NC}"
        echo ""
        echo -e "${BLUE}📋 Next steps:${NC}"
        echo -e "  1. Push the tag: ${CYAN}git push origin ${NEW_VERSION}${NC}"
        echo -e "  2. GitHub Actions will automatically:"
        echo -e "     - Build and test the release"
        echo -e "     - Create GitHub release"
        echo -e "     - Build Docker images"
        echo -e "     - Publish to package managers"
        echo -e "  3. Monitor the release workflow at:"
        echo -e "     ${CYAN}https://github.com/snakeice/kube-tunnel/actions${NC}"
        echo ""
        echo -e "${YELLOW}⚠️  Don't forget to push the commit and tag:${NC}"
        echo -e "${CYAN}git push origin main${NC}"
        echo -e "${CYAN}git push origin ${NEW_VERSION}${NC}"
    fi
}

# Script entry point
main() {
    parse_args "$@"
    prepare_release
}

# Run the script
main "$@"
