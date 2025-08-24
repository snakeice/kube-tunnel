#!/bin/bash

# Health monitoring demo script for kube-tunnel proxy
# Demonstrates health check endpoints and monitoring capabilities

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
PROXY_PORT=${PROXY_PORT:-80}
PROXY_HOST=${PROXY_HOST:-localhost}
PROXY_URL="http://${PROXY_HOST}:${PROXY_PORT}"
USE_VIRTUAL_INTERFACE=${USE_VIRTUAL_INTERFACE:-false}
VIRTUAL_INTERFACE_IP=${VIRTUAL_INTERFACE_IP:-"127.0.0.10"}
VIRTUAL_INTERFACE_NAME=${VIRTUAL_INTERFACE_NAME:-"kube-dummy0"}

echo -e "${BLUE}🏥 kube-tunnel Health Monitoring Demo${NC}"
echo "===================================="
echo -e "Proxy URL: ${CYAN}${PROXY_URL}${NC}"
if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
    echo -e "Virtual Interface: ${CYAN}${VIRTUAL_INTERFACE_NAME} (${VIRTUAL_INTERFACE_IP})${NC}"
fi
echo ""

# Function to check if proxy is running
check_proxy() {
    if ! curl -s --connect-timeout 5 "${PROXY_URL}/health" > /dev/null 2>&1; then
        echo -e "${RED}❌ Proxy is not running at ${PROXY_URL}${NC}"
        echo -e "${YELLOW}💡 Start the proxy with: ./kube-tunnel -port=${PROXY_PORT}${NC}"
        if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
            echo -e "${YELLOW}💡 For virtual interface: sudo ./kube-tunnel -port=${PROXY_PORT} -use-virtual-interface${NC}"
        fi
        exit 1
    fi
}

# Function to check virtual interface status
check_virtual_interface() {
    if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
        echo -e "${YELLOW}🔍 Checking virtual interface status...${NC}"

        # Check if interface exists
        if ip link show "$VIRTUAL_INTERFACE_NAME" &>/dev/null; then
            echo -e "${GREEN}✅ Virtual interface $VIRTUAL_INTERFACE_NAME exists${NC}"

            # Check if IP is configured
            if ip addr show "$VIRTUAL_INTERFACE_NAME" | grep -q "$VIRTUAL_INTERFACE_IP"; then
                echo -e "${GREEN}✅ Virtual interface IP $VIRTUAL_INTERFACE_IP is configured${NC}"

                # Test DNS resolution
                if timeout 5 nslookup kubernetes.default.svc.cluster.local "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
                    echo -e "${GREEN}✅ DNS resolution through virtual interface works${NC}"
                else
                    echo -e "${YELLOW}⚠️  DNS resolution through virtual interface failed${NC}"
                fi
            else
                echo -e "${YELLOW}⚠️  Virtual interface exists but IP not configured${NC}"
            fi
        else
            echo -e "${YELLOW}⚠️  Virtual interface $VIRTUAL_INTERFACE_NAME does not exist${NC}"
        fi
        echo ""
    fi
}

# Function to make a health check request
health_check() {
    local endpoint=$1
    local description=$2

    echo -e "${YELLOW}🔍 Testing: ${description}${NC}"
    echo -e "   Endpoint: ${CYAN}${PROXY_URL}${endpoint}${NC}"

    start_time=$(date +%s%N)
    response=$(curl -s -w "%{http_code}|%{time_total}" "${PROXY_URL}${endpoint}" 2>/dev/null || echo "000|0.000")
    end_time=$(date +%s%N)

    IFS='|' read -r status_code time_total <<< "$response"

    if [[ "$status_code" == "200" ]]; then
        echo -e "   ${GREEN}✅ Status: ${status_code} | Response time: ${time_total}s${NC}"
    else
        echo -e "   ${RED}❌ Status: ${status_code} | Response time: ${time_total}s${NC}"
    fi
    echo ""
}

# Function to demonstrate continuous monitoring
continuous_monitor() {
    echo -e "${BLUE}🔄 Continuous Health Monitoring (Press Ctrl+C to stop)${NC}"
    echo "=================================================="

    while true; do
        timestamp=$(date '+%Y-%m-%d %H:%M:%S')
        response=$(curl -s -w "%{http_code}|%{time_total}" "${PROXY_URL}/health/status" 2>/dev/null || echo "000|0.000")
        IFS='|' read -r status_code time_total <<< "$response"

        if [[ "$status_code" == "200" ]]; then
            echo -e "${timestamp} | ${GREEN}✅ Healthy${NC} | Response: ${time_total}s"
        else
            echo -e "${timestamp} | ${RED}❌ Unhealthy (${status_code})${NC} | Response: ${time_total}s"
        fi

        sleep 2
    done
}

# Main demo function
main() {
    echo -e "${YELLOW}🚀 Starting health monitoring demo...${NC}"
    echo ""

    # Check if proxy is running
    check_proxy

    # Check virtual interface if enabled
    check_virtual_interface

    # Test basic health endpoint
    health_check "/health" "Basic Health Check"

    # Test detailed health status
    health_check "/health/status" "Detailed Health Status"

    # Test health metrics
    health_check "/health/metrics" "Health Metrics"

    # Virtual interface specific health checks
    if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
        echo -e "${YELLOW}🔍 Virtual Interface Health Checks:${NC}"

        # Test DNS health through virtual interface
        start_time=$(date +%s%N)
        if timeout 5 nslookup kubernetes.default.svc.cluster.local "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
            end_time=$(date +%s%N)
            dns_time=$(( (end_time - start_time) / 1000000 ))
            echo -e "${GREEN}✅ DNS health through virtual interface: ${dns_time}ms${NC}"
        else
            echo -e "${RED}❌ DNS health through virtual interface: failed${NC}"
        fi

        # Test service connectivity
        echo -e "${YELLOW}Testing direct service connectivity...${NC}"
        test_services=("kubernetes.default" "kube-dns.kube-system")
        for svc in "${test_services[@]}"; do
            if timeout 3 nslookup "${svc}.svc.cluster.local" "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
                echo -e "  ${GREEN}✅ ${svc}.svc.cluster.local${NC}"
            else
                echo -e "  ${YELLOW}⚠️  ${svc}.svc.cluster.local${NC}"
            fi
        done
        echo ""
    fi

    # Show example service health check
    echo -e "${YELLOW}📝 Example service health checks:${NC}"
    echo -e "   ${CYAN}curl ${PROXY_URL}/my-service.default.svc.cluster.local/health${NC}"
    echo -e "   ${CYAN}curl ${PROXY_URL}/api.default.svc.cluster.local/readiness${NC}"
    echo -e "   ${CYAN}curl ${PROXY_URL}/web.default.svc.cluster.local/liveness${NC}"

    if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
        echo -e "${YELLOW}📝 Direct access through virtual interface:${NC}"
        echo -e "   ${CYAN}curl http://my-service.default.svc.cluster.local/health${NC}"
        echo -e "   ${CYAN}curl http://api.default.svc.cluster.local/readiness${NC}"
    fi
    echo ""

    # Ask if user wants continuous monitoring
    echo -e "${YELLOW}Would you like to start continuous monitoring? (y/N):${NC}"
    read -r response
    if [[ "$response" =~ ^[Yy]$ ]]; then
        continuous_monitor
    fi

    echo -e "${GREEN}✅ Health monitoring demo completed!${NC}"
}

# Handle script arguments
case "${1:-}" in
    --help|-h)
        echo "Health monitoring demo for kube-tunnel proxy"
        echo ""
        echo "Usage: $0 [options]"
        echo ""
        echo "Options:"
        echo "  --help, -h         Show this help message"
        echo "  --continuous, -c   Start continuous monitoring"
        echo "  --port PORT        Set proxy port (default: 80)"
        echo "  --host HOST        Set proxy host (default: localhost)"
        echo "  --virtual-interface Enable virtual interface testing"
        echo "  --vi-ip IP         Set virtual interface IP (default: 127.0.0.10)"
        echo "  --vi-name NAME     Set virtual interface name (default: kube-dummy0)"
        echo ""
        echo "Environment variables:"
        echo "  PROXY_PORT                Proxy port (default: 80)"
        echo "  PROXY_HOST                Proxy host (default: localhost)"
        echo "  USE_VIRTUAL_INTERFACE     Enable virtual interface (default: false)"
        echo "  VIRTUAL_INTERFACE_IP      Virtual interface IP (default: 127.0.0.10)"
        echo "  VIRTUAL_INTERFACE_NAME    Virtual interface name (default: kube-dummy0)"
        exit 0
        ;;
    --continuous|-c)
        check_proxy
        check_virtual_interface
        continuous_monitor
        ;;
    --port)
        PROXY_PORT="$2"
        PROXY_URL="http://${PROXY_HOST}:${PROXY_PORT}"
        shift 2
        main "$@"
        ;;
    --host)
        PROXY_HOST="$2"
        PROXY_URL="http://${PROXY_HOST}:${PROXY_PORT}"
        shift 2
        main "$@"
        ;;
    --virtual-interface)
        USE_VIRTUAL_INTERFACE=true
        shift 1
        main "$@"
        ;;
    --vi-ip)
        VIRTUAL_INTERFACE_IP="$2"
        USE_VIRTUAL_INTERFACE=true
        shift 2
        main "$@"
        ;;
    --vi-name)
        VIRTUAL_INTERFACE_NAME="$2"
        USE_VIRTUAL_INTERFACE=true
        shift 2
        main "$@"
        ;;
    *)
        main "$@"
        ;;
esac
