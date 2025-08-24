#!/bin/bash

# Performance test script for kube-tunnel proxy
# Tests various scenarios and measures latency/throughput

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROXY_PORT=${PROXY_PORT:-80}
PROXY_HOST=${PROXY_HOST:-localhost}
TEST_SERVICE=${TEST_SERVICE:-"httpbin.default.svc.cluster.local"}
CONCURRENT_REQUESTS=${CONCURRENT_REQUESTS:-10}
TOTAL_REQUESTS=${TOTAL_REQUESTS:-100}
USE_VIRTUAL_INTERFACE=${USE_VIRTUAL_INTERFACE:-false}
VIRTUAL_INTERFACE_IP=${VIRTUAL_INTERFACE_IP:-"127.0.0.10"}
VIRTUAL_INTERFACE_NAME=${VIRTUAL_INTERFACE_NAME:-"kube-dummy0"}
DRY_RUN=${1:-false}

echo -e "${BLUE}🚀 kube-tunnel Performance Test Suite${NC}"
echo "=================================="
echo "Proxy: http://${PROXY_HOST}:${PROXY_PORT}"
echo "Test Service: ${TEST_SERVICE}"
echo "Concurrent: ${CONCURRENT_REQUESTS}"
echo "Total Requests: ${TOTAL_REQUESTS}"
echo "Virtual Interface: ${USE_VIRTUAL_INTERFACE}"
if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
    echo "Virtual Interface IP: ${VIRTUAL_INTERFACE_IP}"
    echo "Virtual Interface Name: ${VIRTUAL_INTERFACE_NAME}"
fi
echo ""

# Check if required tools are available
check_tool() {
    if ! command -v $1 &> /dev/null; then
        echo -e "${RED}❌ $1 is required but not installed${NC}"
        echo "Install with: go install github.com/rakyll/hey@latest"
        exit 1
    fi
}

# Check if required tools are available
check_tool() {
    if ! command -v $1 &> /dev/null; then
        echo -e "${RED}❌ $1 is required but not installed${NC}"
        echo "Install with: go install github.com/rakyll/hey@latest"
        exit 1
    fi
}

# Check virtual interface functionality
check_virtual_interface() {
    if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
        echo -e "${YELLOW}🔍 Checking virtual interface support...${NC}"

        # Check if running with sufficient privileges
        if [[ $EUID -ne 0 ]] && ! groups | grep -q docker; then
            echo -e "${YELLOW}⚠️  Virtual interface requires root privileges or Docker group membership${NC}"
            echo "   Run with: sudo $0 or add user to docker group"
            return 1
        fi

        # Check if interface exists
        if ip link show "$VIRTUAL_INTERFACE_NAME" &>/dev/null; then
            echo -e "${GREEN}✅ Virtual interface $VIRTUAL_INTERFACE_NAME exists${NC}"

            # Check if IP is configured
            if ip addr show "$VIRTUAL_INTERFACE_NAME" | grep -q "$VIRTUAL_INTERFACE_IP"; then
                echo -e "${GREEN}✅ Virtual interface IP $VIRTUAL_INTERFACE_IP is configured${NC}"
            else
                echo -e "${YELLOW}⚠️  Virtual interface exists but IP $VIRTUAL_INTERFACE_IP not configured${NC}"
            fi
        else
            echo -e "${YELLOW}⚠️  Virtual interface $VIRTUAL_INTERFACE_NAME does not exist${NC}"
            echo "   Will be created automatically by kube-tunnel"
        fi

        # Test DNS resolution through virtual interface
        echo -e "${YELLOW}🔍 Testing DNS resolution through virtual interface...${NC}"
        if timeout 5 nslookup kubernetes.default.svc.cluster.local "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
            echo -e "${GREEN}✅ DNS resolution through virtual interface works${NC}"
        else
            echo -e "${YELLOW}⚠️  DNS resolution through virtual interface not available${NC}"
            echo "   This is normal if kube-tunnel is not running"
        fi
    fi

    return 0
}

# Handle dry run mode
if [[ "$DRY_RUN" == "--dry-run" || "$DRY_RUN" == "true" ]]; then
    echo -e "${BLUE}🧪 Dry run mode - validating test configuration${NC}"
    echo "=============================================="

    check_tool "hey"
    check_tool "curl"
    check_virtual_interface

    echo -e "${GREEN}✅ Performance test script validation completed${NC}"
    exit 0
fi

check_tool "hey"
check_tool "curl"
check_virtual_interface

# Test 1: Health check
echo -e "${YELLOW}📊 Test 1: Health Check${NC}"
start_time=$(date +%s%N)
response=$(curl -s -w "%{http_code}" -o /dev/null http://${PROXY_HOST}:${PROXY_PORT}/health)
end_time=$(date +%s%N)
health_latency=$(( (end_time - start_time) / 1000000 ))

if [ "$response" = "200" ]; then
    echo -e "${GREEN}✅ Health check passed (${health_latency}ms)${NC}"
else
    echo -e "${RED}❌ Health check failed (HTTP $response)${NC}"
    exit 1
fi

# Test 1.5: Virtual interface DNS test (if enabled)
if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
    echo -e "${YELLOW}📊 Test 1.5: Virtual Interface DNS Resolution${NC}"

    # Test DNS resolution through virtual interface
    start_time=$(date +%s%N)
    if timeout 10 nslookup kubernetes.default.svc.cluster.local "$VIRTUAL_INTERFACE_IP" &>/dev/null; then
        end_time=$(date +%s%N)
        dns_latency=$(( (end_time - start_time) / 1000000 ))
        echo -e "${GREEN}✅ DNS resolution through virtual interface (${dns_latency}ms)${NC}"

        # Test direct service access through virtual interface
        if timeout 10 curl -s --connect-timeout 5 "http://${TEST_SERVICE}/" &>/dev/null; then
            echo -e "${GREEN}✅ Direct service access through virtual interface${NC}"
        else
            echo -e "${YELLOW}⚠️  Direct service access through virtual interface failed${NC}"
            echo "   This may be expected if services are not running"
        fi
    else
        echo -e "${YELLOW}⚠️  DNS resolution through virtual interface failed${NC}"
        echo "   Continuing with proxy-based tests..."
    fi
fi

# Test 2: First request latency (cold start)
echo -e "${YELLOW}📊 Test 2: First Request Latency (Cold Start)${NC}"
start_time=$(date +%s%N)
response=$(curl -s -w "%{http_code},%{time_total}" -o /dev/null http://${TEST_SERVICE}:${PROXY_PORT}/health)
end_time=$(date +%s%N)
cold_start_time=$(echo $response | cut -d',' -f2)
cold_start_ms=$(echo "scale=0; $cold_start_time * 1000" | bc)

if [[ $response == 200* ]]; then
    echo -e "${GREEN}✅ Cold start request: ${cold_start_ms}ms${NC}"
else
    echo -e "${RED}❌ Cold start failed${NC}"
    exit 1
fi

# Test 3: Warm request latency
echo -e "${YELLOW}📊 Test 3: Warm Request Latency${NC}"
warm_times=()
for i in {1..5}; do
    response=$(curl -s -w "%{time_total}" -o /dev/null http://${TEST_SERVICE}:${PROXY_PORT}/health)
    warm_time_ms=$(echo "scale=0; $response * 1000" | bc)
    warm_times+=($warm_time_ms)
done

# Calculate average warm time
warm_avg=0
for time in "${warm_times[@]}"; do
    warm_avg=$((warm_avg + time))
done
warm_avg=$((warm_avg / ${#warm_times[@]}))
echo -e "${GREEN}✅ Average warm request: ${warm_avg}ms${NC}"

# Test 4: Throughput test
echo -e "${YELLOW}📊 Test 4: Throughput Test${NC}"
echo "Running ${TOTAL_REQUESTS} requests with ${CONCURRENT_REQUESTS} concurrent connections..."

hey_output=$(hey -n ${TOTAL_REQUESTS} -c ${CONCURRENT_REQUESTS} -q 0 -t 30 \
    http://${TEST_SERVICE}:${PROXY_PORT}/get 2>&1)

# Parse hey output
total_time=$(echo "$hey_output" | grep "Total:" | awk '{print $2}' | sed 's/s//')
requests_per_sec=$(echo "$hey_output" | grep "Requests/sec:" | awk '{print $2}')
avg_latency=$(echo "$hey_output" | grep "Average:" | awk '{print $2}' | sed 's/s//')
p95_latency=$(echo "$hey_output" | grep "95%" | awk '{print $2}' | sed 's/s//')

echo -e "${GREEN}✅ Throughput Results:${NC}"
echo "  Requests/sec: ${requests_per_sec}"
echo "  Average latency: $(echo "scale=0; $avg_latency * 1000" | bc)ms"
echo "  95th percentile: $(echo "scale=0; $p95_latency * 1000" | bc)ms"
echo "  Total time: ${total_time}s"

# Test 5: Connection reuse test
echo -e "${YELLOW}📊 Test 5: Connection Reuse Test${NC}"
echo "Testing with keep-alive connections..."

keepalive_output=$(hey -n 50 -c 5 -k -t 30 \
    http://${TEST_SERVICE}:${PROXY_PORT}/health 2>&1)

keepalive_rps=$(echo "$keepalive_output" | grep "Requests/sec:" | awk '{print $2}')
keepalive_avg=$(echo "$keepalive_output" | grep "Average:" | awk '{print $2}' | sed 's/s//')

echo -e "${GREEN}✅ Keep-alive Results:${NC}"
echo "  Requests/sec: ${keepalive_rps}"
echo "  Average latency: $(echo "scale=0; $keepalive_avg * 1000" | bc)ms"

# Test 6: Cache efficiency test
echo -e "${YELLOW}📊 Test 6: Cache Efficiency Test${NC}"
echo "Testing multiple services to check port-forward caching..."

services=("httpbin.default" "nginx.default" "redis.default")
cache_times=()

for service in "${services[@]}"; do
    echo "  Testing ${service}.svc.cluster.local..."
    start_time=$(date +%s%N)

    # Test through proxy first
    response=$(curl -s -w "%{http_code}" -o /dev/null http://${service}.svc.cluster.local:${PROXY_PORT}/get 2>/dev/null || curl -s -w "%{http_code}" -o /dev/null http://${service}.svc.cluster.local:${PROXY_PORT}/ 2>/dev/null || echo "404")
    end_time=$(date +%s%N)
    cache_time=$(( (end_time - start_time) / 1000000 ))
    cache_times+=($cache_time)

    if [[ $response == 2* ]] || [[ $response == 4* ]]; then
        echo "    ✅ ${service} (proxy): ${cache_time}ms"
    else
        echo "    ❌ ${service} (proxy): failed"
    fi

    # Test through virtual interface if enabled
    if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
        start_time=$(date +%s%N)
        # Test direct access through virtual interface DNS
        if timeout 5 curl -s --connect-timeout 2 "http://${service}.svc.cluster.local/" &>/dev/null; then
            end_time=$(date +%s%N)
            vi_time=$(( (end_time - start_time) / 1000000 ))
            echo "    ✅ ${service} (virtual interface): ${vi_time}ms"
        else
            echo "    ⚠️  ${service} (virtual interface): not accessible"
        fi
    fi
done

# Performance summary (will be updated after Test 7)
health_summary=""

# Performance rating
echo ""
echo -e "${BLUE}🎯 Performance Rating${NC}"
echo "===================="

rating=0
comments=()

# Rate cold start performance
if (( $(echo "$cold_start_ms < 200" | bc -l) )); then
    rating=$((rating + 2))
    comments+=("✅ Excellent cold start performance (<200ms)")
elif (( $(echo "$cold_start_ms < 500" | bc -l) )); then
    rating=$((rating + 1))
    comments+=("✅ Good cold start performance (<500ms)")
else
    comments+=("⚠️  Slow cold start performance (>500ms)")
fi

# Rate warm performance
if (( warm_avg < 10 )); then
    rating=$((rating + 2))
    comments+=("✅ Excellent warm request performance (<10ms)")
elif (( warm_avg < 25 )); then
    rating=$((rating + 1))
    comments+=("✅ Good warm request performance (<25ms)")
else
    comments+=("⚠️  Slow warm request performance (>25ms)")
fi

# Rate throughput
if (( $(echo "$requests_per_sec > 500" | bc -l) )); then
    rating=$((rating + 2))
    comments+=("✅ Excellent throughput (>500 req/s)")
elif (( $(echo "$requests_per_sec > 200" | bc -l) )); then
    rating=$((rating + 1))
    comments+=("✅ Good throughput (>200 req/s)")
else
    comments+=("⚠️  Low throughput (<200 req/s)")
fi

# Display rating
case $rating in
    6) echo -e "${GREEN}🌟 EXCELLENT (6/6)${NC}" ;;
    4|5) echo -e "${GREEN}🎯 GOOD (${rating}/6)${NC}" ;;
    2|3) echo -e "${YELLOW}⚡ AVERAGE (${rating}/6)${NC}" ;;
    *) echo -e "${RED}🐌 NEEDS OPTIMIZATION (${rating}/6)${NC}" ;;
esac

echo ""
for comment in "${comments[@]}"; do
    echo "$comment"
done

# Optimization suggestions
echo ""
echo -e "${BLUE}💡 Optimization Suggestions${NC}"
echo "============================"

if (( cold_start_ms > 500 )); then
    echo "• Reduce port-forward setup time:"
    echo "  export PROXY_MAX_RETRIES=1"
    echo "  export PROXY_RETRY_DELAY_MS=50"
fi

if (( warm_avg > 25 )); then
    echo "• Improve warm request performance:"
    echo "  export SKIP_HEALTH_CHECK=true"
    echo "  export FORCE_HTTP2=true"
fi

if (( $(echo "$requests_per_sec < 200" | bc -l) )); then
    echo "• Increase throughput:"
    echo "  export MAX_IDLE_CONNS=300"
    echo "  export MAX_IDLE_CONNS_PER_HOST=100"
    echo "  export MAX_CONCURRENT_STREAMS=2000"
fi

# Virtual interface specific optimizations
if [[ "$USE_VIRTUAL_INTERFACE" == "true" ]]; then
    echo "• Virtual Interface optimizations:"
    echo "  export KTUN_USE_VIRTUAL=true"
    echo "  export KTUN_VIRTUAL_INTERFACE_IP=${VIRTUAL_INTERFACE_IP}"
    echo "  export KTUN_VIRTUAL_INTERFACE_NAME=${VIRTUAL_INTERFACE_NAME}"
    echo "  # Use direct DNS resolution for better performance"
elif [[ "$USE_VIRTUAL_INTERFACE" == "false" ]]; then
    echo "• Consider enabling virtual interface for better DNS performance:"
    echo "  export USE_VIRTUAL_INTERFACE=true"
    echo "  sudo ./kube-tunnel  # Requires elevated privileges"
    echo "  # Or run in Docker with --privileged flag"
fi

# Test 7: Health monitoring performance
echo -e "${YELLOW}📊 Test 7: Health Monitoring Performance${NC}"
echo "Testing background health monitoring API endpoints..."

# Test health status endpoint
health_start=$(date +%s%N)
health_response=$(curl -s -w "%{http_code},%{time_total}" -o /dev/null http://${PROXY_HOST}:${PROXY_PORT}/health/status)
health_end=$(date +%s%N)
health_code=$(echo $health_response | cut -d',' -f1)
health_time=$(echo $health_response | cut -d',' -f2)
health_ms=$(echo "scale=0; $health_time * 1000" | bc)

if [[ $health_code == 200 ]]; then
    echo -e "${GREEN}✅ Health status API: ${health_ms}ms${NC}"

    # Rate health monitoring performance
    if (( health_ms < 10 )); then
        rating=$((rating + 1))
        comments+=("✅ Excellent health monitoring performance (<10ms)")
    elif (( health_ms < 25 )); then
        comments+=("✅ Good health monitoring performance (<25ms)")
    else
        comments+=("⚠️  Slow health monitoring API (>25ms)")
    fi
else
    echo -e "${YELLOW}⚠️  Health status API not available${NC}"
fi

# Test health metrics endpoint
metrics_start=$(date +%s%N)
metrics_response=$(curl -s -w "%{http_code},%{time_total}" -o /dev/null http://${PROXY_HOST}:${PROXY_PORT}/health/metrics)
metrics_end=$(date +%s%N)
metrics_code=$(echo $metrics_response | cut -d',' -f1)
metrics_time=$(echo $metrics_response | cut -d',' -f2)
metrics_ms=$(echo "scale=0; $metrics_time * 1000" | bc)

if [[ $metrics_code == 200 ]]; then
    echo -e "${GREEN}✅ Health metrics API: ${metrics_ms}ms${NC}"

    # Get health monitoring stats
    if command -v jq &> /dev/null; then
        health_stats=$(curl -s http://${PROXY_HOST}:${PROXY_PORT}/health/metrics)
        total_services=$(echo "$health_stats" | jq -r '.total_services // 0')
        healthy_services=$(echo "$health_stats" | jq -r '.healthy_services // 0')
        avg_response=$(echo "$health_stats" | jq -r '.response_times.average_ms // 0')
        monitor_enabled=$(echo "$health_stats" | jq -r '.monitor_enabled // false')

        echo "  Monitored services: ${total_services}"
        echo "  Healthy services: ${healthy_services}"
        echo "  Average health check: ${avg_response}ms"
        echo "  Monitor enabled: ${monitor_enabled}"
    fi
else
    echo -e "${YELLOW}⚠️  Health metrics API not available${NC}"
fi

# Add health API performance to summary
if [[ $health_code == 200 ]]; then
    echo -e "Health API Latency:      ${health_ms}ms"
fi

# Final performance summary
echo ""
echo -e "${BLUE}📈 Performance Summary${NC}"
echo "======================"
echo -e "Health Check Latency:    ${health_latency}ms"
echo -e "Cold Start Latency:      ${cold_start_ms}ms"
echo -e "Warm Request Latency:    ${warm_avg}ms"
echo -e "Throughput:              ${requests_per_sec} req/s"
echo -e "P95 Latency:             $(echo "scale=0; $p95_latency * 1000" | bc)ms"
echo -e "Keep-alive Throughput:   ${keepalive_rps} req/s"
if [[ $health_code == 200 ]]; then
    echo -e "Health API Latency:      ${health_ms}ms"
fi

echo ""
echo "For more optimization options, see PERFORMANCE.md"
echo "For health monitoring demo, run: ./scripts/health-demo.sh"
echo -e "${GREEN}🏁 Performance test completed!${NC}"
