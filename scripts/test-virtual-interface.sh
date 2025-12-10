#!/bin/bash

# Virtual interface test script for kube-tunnel
# Tests virtual interface creation, DNS resolution, and cleanup
# Supports both Linux and macOS

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Detect platform
OS_TYPE=$(uname -s)
case "$OS_TYPE" in
    Darwin*)
        PLATFORM="macos"
        LOOPBACK_INTERFACE="lo0"
        ;;
    Linux*)
        PLATFORM="linux"
        LOOPBACK_INTERFACE="lo"
        ;;
    *)
        echo -e "${RED}❌ Unsupported platform: $OS_TYPE${NC}"
        exit 1
        ;;
esac

# Configuration
VIRTUAL_INTERFACE_NAME=${VIRTUAL_INTERFACE_NAME:-"kube-dummy0-test"}
VIRTUAL_INTERFACE_IP=${VIRTUAL_INTERFACE_IP:-"127.0.0.11"}
TEST_MODE=${1:-"quick"}

echo -e "${BLUE}🧪 Virtual Interface Test Suite${NC}"
echo "================================="
echo "Platform: ${PLATFORM} (${OS_TYPE})"
echo "Interface: ${VIRTUAL_INTERFACE_NAME}"
echo "IP: ${VIRTUAL_INTERFACE_IP}"
echo "Loopback: ${LOOPBACK_INTERFACE}"
echo "Mode: ${TEST_MODE}"
echo ""

# Function to check if running as root or with proper privileges
check_privileges() {
    if [[ $EUID -ne 0 ]] && ! groups | grep -q docker; then
        echo -e "${RED}❌ This test requires root privileges or Docker group membership${NC}"
        echo "Run with: sudo $0 or add user to docker group"
        exit 1
    fi
}

# Function to cleanup interface if it exists
cleanup_interface() {
    if [ "$PLATFORM" == "macos" ]; then
        if ifconfig "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${YELLOW}🧹 Cleaning up IP alias from loopback interface${NC}"
            ifconfig "$LOOPBACK_INTERFACE" -alias "$VIRTUAL_INTERFACE_IP" 2>/dev/null || true
        fi
    else
        if ip addr show "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${YELLOW}🧹 Cleaning up IP address from loopback interface${NC}"
            ip addr del "$VIRTUAL_INTERFACE_IP/32" dev "$LOOPBACK_INTERFACE" 2>/dev/null || true
        fi
    fi
}

# Function to create test interface
create_test_interface() {
    echo -e "${YELLOW}🔧 Assigning IP to loopback interface${NC}"

    if [ "$PLATFORM" == "macos" ]; then
        # macOS: Add alias to lo0
        if ! ifconfig "$LOOPBACK_INTERFACE" alias "$VIRTUAL_INTERFACE_IP" netmask 255.255.255.255; then
            echo -e "${RED}❌ Failed to add IP alias to loopback interface${NC}"
            return 1
        fi
    else
        # Linux: Add IP address to lo
        if ! ip addr add "$VIRTUAL_INTERFACE_IP/32" dev "$LOOPBACK_INTERFACE"; then
            echo -e "${RED}❌ Failed to add IP address to loopback interface${NC}"
            return 1
        fi
    fi

    echo -e "${GREEN}✅ IP assigned successfully to loopback interface${NC}"
    return 0
}

# Function to test interface configuration
test_interface_config() {
    echo -e "${YELLOW}🔍 Testing interface configuration${NC}"

    # Check if IP is configured on loopback
    if [ "$PLATFORM" == "macos" ]; then
        if ! ifconfig "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${RED}❌ IP address not configured on loopback interface${NC}"
            return 1
        fi
    else
        if ! ip addr show "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${RED}❌ IP address not configured on loopback interface${NC}"
            return 1
        fi
    fi

    echo -e "${GREEN}✅ Interface configuration is correct${NC}"
    return 0
}

# Function to test DNS functionality (mock test)
test_dns_functionality() {
    echo -e "${YELLOW}🔍 Testing DNS functionality${NC}"


    # Test that we can bind to the interface (mock DNS server test)
    if timeout 2 nc -l -s "$VIRTUAL_INTERFACE_IP" -p 15353 &>/dev/null &
    then
        local nc_pid=$!
        sleep 0.5
        if kill -0 $nc_pid 2>/dev/null; then
            kill $nc_pid 2>/dev/null || true
            echo -e "${GREEN}✅ Can bind to interface for DNS service${NC}"
        else
            echo -e "${RED}❌ Cannot bind to interface${NC}"
            return 1
        fi
    else
        echo -e "${YELLOW}⚠️  netcat not available, skipping bind test${NC}"
    fi

    return 0
}

# Function to run quick tests
run_quick_tests() {
    echo -e "${BLUE}Running quick virtual interface tests...${NC}"

    cleanup_interface

    if ! create_test_interface; then
        echo -e "${RED}❌ Quick test failed: Could not create interface${NC}"
        return 1
    fi

    if ! test_interface_config; then
        echo -e "${RED}❌ Quick test failed: Interface configuration invalid${NC}"
        cleanup_interface
        return 1
    fi

    cleanup_interface
    echo -e "${GREEN}✅ Quick virtual interface tests passed${NC}"
    return 0
}

# Function to run comprehensive tests
run_comprehensive_tests() {
    echo -e "${BLUE}Running comprehensive virtual interface tests...${NC}"

    cleanup_interface

    # Test 1: Interface creation
    echo -e "${YELLOW}Test 1: Interface Creation${NC}"
    if ! create_test_interface; then
        echo -e "${RED}❌ Test 1 failed${NC}"
        return 1
    fi

    # Test 2: Configuration validation
    echo -e "${YELLOW}Test 2: Configuration Validation${NC}"
    if ! test_interface_config; then
        echo -e "${RED}❌ Test 2 failed${NC}"
        cleanup_interface
        return 1
    fi

    # Test 3: DNS functionality
    echo -e "${YELLOW}Test 3: DNS Functionality${NC}"
    if ! test_dns_functionality; then
        echo -e "${RED}❌ Test 3 failed${NC}"
        cleanup_interface
        return 1
    fi

    # Test 4: Interface persistence
    echo -e "${YELLOW}Test 4: Interface Persistence${NC}"
    sleep 2
    if ! test_interface_config; then
        echo -e "${RED}❌ Test 4 failed: Interface configuration changed${NC}"
        cleanup_interface
        return 1
    fi

    # Test 5: Cleanup
    echo -e "${YELLOW}Test 5: Cleanup${NC}"
    cleanup_interface

    # Verify cleanup based on platform
    if [ "$PLATFORM" == "macos" ]; then
        if ifconfig "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${RED}❌ Test 5 failed: IP alias still exists after cleanup${NC}"
            return 1
        fi
    else
        if ip addr show "$LOOPBACK_INTERFACE" | grep -q "$VIRTUAL_INTERFACE_IP"; then
            echo -e "${RED}❌ Test 5 failed: IP address still exists after cleanup${NC}"
            return 1
        fi
    fi

    echo -e "${GREEN}✅ All comprehensive virtual interface tests passed${NC}"
    return 0
}

# Main execution
main() {
    # Check if we can run the tests
    if [[ "$TEST_MODE" != "dry-run" ]]; then
        check_privileges
    fi

    case "$TEST_MODE" in
        "quick")
            if [[ "$TEST_MODE" == "dry-run" ]]; then
                echo -e "${GREEN}✅ Virtual interface test script validation completed${NC}"
                exit 0
            fi
            run_quick_tests
            ;;
        "comprehensive")
            run_comprehensive_tests
            ;;
        "dry-run")
            echo -e "${GREEN}✅ Virtual interface test script validation completed${NC}"
            echo "Available modes: quick, comprehensive"
            exit 0
            ;;
        *)
            echo -e "${RED}❌ Unknown test mode: $TEST_MODE${NC}"
            echo "Available modes: quick, comprehensive, dry-run"
            exit 1
            ;;
    esac
}

# Handle script arguments
case "${1:-}" in
    --help|-h)
        echo "Virtual interface test script for kube-tunnel"
        echo "Supports both Linux and macOS"
        echo ""
        echo "Usage: $0 [mode]"
        echo ""
        echo "Modes:"
        echo "  quick          Run quick interface tests (default)"
        echo "  comprehensive  Run comprehensive test suite"
        echo "  dry-run        Validate script without running tests"
        echo "  --help, -h     Show this help message"
        echo ""
        echo "Environment variables:"
        echo "  VIRTUAL_INTERFACE_NAME  Interface name (default: kube-dummy0-test)"
        echo "  VIRTUAL_INTERFACE_IP    Interface IP (default: 127.0.0.11)"
        echo ""
        echo "Platform-specific behavior:"
        echo "  Linux:  Uses 'ip' command to manage loopback (lo) addresses"
        echo "  macOS:  Uses 'ifconfig' to manage loopback (lo0) aliases"
        echo ""
        echo "Note: This script requires root privileges (sudo)"
        exit 0
        ;;
    *)
        main
        ;;
esac
