#!/bin/bash

# Virtual interface test script for kube-tunnel
# Tests virtual interface creation, DNS resolution, and cleanup

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
VIRTUAL_INTERFACE_NAME=${VIRTUAL_INTERFACE_NAME:-"kube-dummy0-test"}
VIRTUAL_INTERFACE_IP=${VIRTUAL_INTERFACE_IP:-"127.0.0.11"}
TEST_MODE=${1:-"quick"}

echo -e "${BLUE}🧪 Virtual Interface Test Suite${NC}"
echo "================================="
echo "Interface: ${VIRTUAL_INTERFACE_NAME}"
echo "IP: ${VIRTUAL_INTERFACE_IP}"
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
    if ip link show "$VIRTUAL_INTERFACE_NAME" &>/dev/null; then
        echo -e "${YELLOW}🧹 Cleaning up existing interface ${VIRTUAL_INTERFACE_NAME}${NC}"
        ip link delete "$VIRTUAL_INTERFACE_NAME" 2>/dev/null || true
    fi
}

# Function to create test interface
create_test_interface() {
    echo -e "${YELLOW}🔧 Creating test interface ${VIRTUAL_INTERFACE_NAME}${NC}"

    # Create dummy interface
    if ! ip link add "$VIRTUAL_INTERFACE_NAME" type dummy; then
        echo -e "${RED}❌ Failed to create dummy interface${NC}"
        return 1
    fi

    # Add IP address
    if ! ip addr add "$VIRTUAL_INTERFACE_IP/32" dev "$VIRTUAL_INTERFACE_NAME"; then
        echo -e "${RED}❌ Failed to add IP address${NC}"
        ip link delete "$VIRTUAL_INTERFACE_NAME" 2>/dev/null || true
        return 1
    fi

    # Bring interface up
    if ! ip link set "$VIRTUAL_INTERFACE_NAME" up; then
        echo -e "${RED}❌ Failed to bring interface up${NC}"
        ip link delete "$VIRTUAL_INTERFACE_NAME" 2>/dev/null || true
        return 1
    fi

    echo -e "${GREEN}✅ Test interface created successfully${NC}"
    return 0
}

# Function to test interface configuration
test_interface_config() {
    echo -e "${YELLOW}🔍 Testing interface configuration${NC}"

    # Check if interface exists
    if ! ip link show "$VIRTUAL_INTERFACE_NAME" &>/dev/null; then
        echo -e "${RED}❌ Interface does not exist${NC}"
        return 1
    fi

    # Check if IP is configured
    if ! ip addr show "$VIRTUAL_INTERFACE_NAME" | grep -q "$VIRTUAL_INTERFACE_IP"; then
        echo -e "${RED}❌ IP address not configured${NC}"
        return 1
    fi

    # Check if interface is up
    if ! ip link show "$VIRTUAL_INTERFACE_NAME" | grep -q "state UP"; then
        echo -e "${RED}❌ Interface is not up${NC}"
        return 1
    fi

    echo -e "${GREEN}✅ Interface configuration is correct${NC}"
    return 0
}

# Function to test DNS functionality (mock test)
test_dns_functionality() {
    echo -e "${YELLOW}🔍 Testing DNS functionality${NC}"

    # Test basic connectivity to the interface IP
    if ping -c 1 -W 1 "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
        echo -e "${GREEN}✅ Interface IP is reachable${NC}"
    else
        echo -e "${RED}❌ Interface IP is not reachable${NC}"
        return 1
    fi

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
    if ip link show "$VIRTUAL_INTERFACE_NAME" &>/dev/null; then
        echo -e "${RED}❌ Test 5 failed: Interface still exists after cleanup${NC}"
        return 1
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
        echo "Note: This script requires root privileges or Docker group membership"
        exit 0
        ;;
    *)
        main
        ;;
esac
