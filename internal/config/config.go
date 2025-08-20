package config

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

const (
	Kb                 = 1024
	Mb                 = 1024 * Kb
	Gb                 = 1024 * Mb
	dummyInterfaceType = "dummy"
)

type Config struct {
	Performance PerformanceConfig
	Health      HealthConfig
	Network     NetworkConfig
}

type PerformanceConfig struct {
	SkipHealthCheck              bool
	ForceHTTP2                   bool
	DisableProtocolFallback      bool
	MaxIdleConns                 int
	MaxIdleConnsPerHost          int
	MaxConnsPerHost              int
	ReadTimeout                  time.Duration
	WriteTimeout                 time.Duration
	IdleTimeout                  time.Duration
	MaxConcurrentStreams         uint32
	MaxFrameSize                 uint32
	MaxUploadBufferPerConnection int32
	MaxUploadBufferPerStream     int32
	GRPCTimeout                  string
}

type HealthConfig struct {
	Enabled       bool
	CheckInterval time.Duration
	Timeout       time.Duration
	MaxFailures   int
}

type NetworkConfig struct {
	// Virtual interface configuration for dummy interfaces
	UseVirtualInterface  bool
	VirtualInterfaceName string
	VirtualInterfaceIP   string
	InterfaceType        string // "dummy" only

	// DNS configuration
	DNSBindIP string

	// Port forwarding configuration
	PortForwardBindIP string

	// IP range configuration for finding free IPs
	CustomIPRanges []string // Custom IP ranges to search for free IPs
}

func GetConfig() *Config {
	perf := createDefaultPerformanceConfig()
	health := createDefaultHealthConfig()
	network := createDefaultNetworkConfig()

	// Apply environment overrides
	perf = applyPerformanceOverrides(perf)
	health = applyHealthOverrides(health)
	network = applyNetworkOverrides(network)

	// Validate configurations
	perf = validatePerformanceConfig(perf)
	health = validateHealthConfig(health)
	network = validateNetworkConfig(network)

	return &Config{Performance: perf, Health: health, Network: network}
}

// createDefaultPerformanceConfig creates the default performance configuration.
func createDefaultPerformanceConfig() PerformanceConfig {
	return PerformanceConfig{
		SkipHealthCheck:              false,
		ForceHTTP2:                   true,
		DisableProtocolFallback:      false,
		MaxIdleConns:                 200,
		MaxIdleConnsPerHost:          50,
		MaxConnsPerHost:              100,
		ReadTimeout:                  30 * time.Second,
		WriteTimeout:                 30 * time.Second,
		IdleTimeout:                  120 * time.Second,
		MaxConcurrentStreams:         1000,
		MaxFrameSize:                 512 * Kb,
		GRPCTimeout:                  "30S",
		MaxUploadBufferPerConnection: 1 * Mb,
		MaxUploadBufferPerStream:     1 * Mb,
	}
}

// createDefaultHealthConfig creates the default health configuration.
func createDefaultHealthConfig() HealthConfig {
	return HealthConfig{
		Enabled:       true,
		CheckInterval: 30 * time.Second,
		Timeout:       2 * time.Second,
		MaxFailures:   3,
	}
}

// createDefaultNetworkConfig creates the default network configuration.
func createDefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		UseVirtualInterface:  true,
		VirtualInterfaceName: "kube-dummy0",
		VirtualInterfaceIP:   "10.8.0.1",
		InterfaceType:        dummyInterfaceType,
		DNSBindIP:            "",
		PortForwardBindIP:    "",
		CustomIPRanges: []string{
			"10.8.0.0/24",
			"10.9.0.0/24",
			"10.10.0.0/24",
			"172.16.0.0/24",
			"192.168.100.0/24",
		},
	}
}

// applyPerformanceOverrides applies environment variable overrides to performance config.
func applyPerformanceOverrides(perf PerformanceConfig) PerformanceConfig {
	perf.SkipHealthCheck = getEnvBool("SKIP_HEALTH_CHECK", perf.SkipHealthCheck)
	perf.ForceHTTP2 = !getEnvBool("FORCE_HTTP2", !perf.ForceHTTP2)
	perf.DisableProtocolFallback = getEnvBool(
		"DISABLE_PROTOCOL_FALLBACK",
		perf.DisableProtocolFallback,
	)
	perf.MaxIdleConns = getEnvInt("MAX_IDLE_CONNS", perf.MaxIdleConns)
	perf.MaxIdleConnsPerHost = getEnvInt("MAX_IDLE_CONNS_PER_HOST", perf.MaxIdleConnsPerHost)
	perf.MaxConnsPerHost = getEnvInt("MAX_CONNS_PER_HOST", perf.MaxConnsPerHost)
	perf.ReadTimeout = getEnvDuration("READ_TIMEOUT", perf.ReadTimeout)
	perf.WriteTimeout = getEnvDuration("WRITE_TIMEOUT", perf.WriteTimeout)
	perf.IdleTimeout = getEnvDuration("IDLE_TIMEOUT", perf.IdleTimeout)
	perf.MaxConcurrentStreams = getEnvUint32("MAX_CONCURRENT_STREAMS", perf.MaxConcurrentStreams)
	perf.MaxFrameSize = getEnvUint32("MAX_FRAME_SIZE", perf.MaxFrameSize)
	perf.MaxUploadBufferPerConnection = getEnvInt32(
		"MAX_UPLOAD_BUFFER_PER_CONNECTION",
		perf.MaxUploadBufferPerConnection,
	)
	perf.MaxUploadBufferPerStream = getEnvInt32(
		"MAX_UPLOAD_BUFFER_PER_STREAM",
		perf.MaxUploadBufferPerStream,
	)

	if val := os.Getenv("GRPC_TIMEOUT"); val != "" {
		perf.GRPCTimeout = val
	}
	return perf
}

// applyHealthOverrides applies environment variable overrides to health config.
func applyHealthOverrides(health HealthConfig) HealthConfig {
	health.Enabled = !getEnvBool("HEALTH_MONITOR_ENABLED", !health.Enabled)
	health.CheckInterval = getEnvDuration("HEALTH_CHECK_INTERVAL", health.CheckInterval)
	health.Timeout = getEnvDuration("HEALTH_CHECK_TIMEOUT", health.Timeout)
	health.MaxFailures = getEnvInt("HEALTH_MAX_FAILURES", health.MaxFailures)
	return health
}

// applyNetworkOverrides applies environment variable overrides to network config.
func applyNetworkOverrides(network NetworkConfig) NetworkConfig {
	if val := os.Getenv("DNS_BIND_IP"); val != "" {
		network.DNSBindIP = val
	}
	if val := os.Getenv("PORT_FORWARD_BIND_IP"); val != "" {
		network.PortForwardBindIP = val
	}
	network.UseVirtualInterface = getEnvBool(
		"KUBE_TUNNEL_USE_VIRTUAL_INTERFACE",
		network.UseVirtualInterface,
	)
	if val := os.Getenv("KUBE_TUNNEL_VIRTUAL_INTERFACE_NAME"); val != "" {
		network.VirtualInterfaceName = val
	}
	if val := os.Getenv("KUBE_TUNNEL_VIRTUAL_INTERFACE_IP"); val != "" {
		network.VirtualInterfaceIP = val
	}

	// Interface type validation - only allow dummy
	if val := os.Getenv("KUBE_TUNNEL_INTERFACE_TYPE"); val != "" {
		if val == dummyInterfaceType {
			network.InterfaceType = val
		} else {
			logger.Log.Warnf("Invalid interface type '%s', using default '%s'", val, dummyInterfaceType)
		}
	}

	// Custom IP ranges for finding free IPs
	if val := os.Getenv("KUBE_TUNNEL_CUSTOM_IP_RANGES"); val != "" {
		ranges := strings.Split(val, ",")
		for i, rangeStr := range ranges {
			ranges[i] = strings.TrimSpace(rangeStr)
		}
		network.CustomIPRanges = ranges
	}
	return network
}

// validatePerformanceConfig validates performance configuration values.
func validatePerformanceConfig(perf PerformanceConfig) PerformanceConfig {
	if perf.MaxIdleConns < 1 {
		perf.MaxIdleConns = 1
	}
	if perf.MaxIdleConnsPerHost < 1 {
		perf.MaxIdleConnsPerHost = 1
	}
	if perf.MaxConnsPerHost < 1 {
		perf.MaxConnsPerHost = 1
	}
	if perf.MaxConcurrentStreams < 1 {
		perf.MaxConcurrentStreams = 1
	}
	if perf.MaxFrameSize < 16384 {
		perf.MaxFrameSize = 16384
	}
	return perf
}

// validateHealthConfig validates health configuration values.
func validateHealthConfig(health HealthConfig) HealthConfig {
	if health.CheckInterval < time.Second {
		health.CheckInterval = 1 * time.Second
	}
	if health.Timeout < 100*time.Millisecond {
		health.Timeout = 100 * time.Millisecond
	}
	if health.MaxFailures < 1 {
		health.MaxFailures = 1
	}
	return health
}

// validateNetworkConfig validates network configuration values.
func validateNetworkConfig(network NetworkConfig) NetworkConfig {
	if network.InterfaceType != dummyInterfaceType {
		logger.Log.Warnf(
			"Invalid interface type: %s, using default '%s'",
			network.InterfaceType,
			dummyInterfaceType,
		)
		network.InterfaceType = dummyInterfaceType
	}
	return network
}

func getEnvBool(key string, def bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val == "true"
}

func getEnvInt(key string, def int) int {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	if parsed, err := strconv.Atoi(val); err == nil {
		return parsed
	}
	return def
}

func getEnvInt32(key string, def int32) int32 {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	if parsed, err := strconv.ParseInt(val, 10, 32); err == nil {
		return int32(parsed)
	}
	return def
}

func getEnvUint32(key string, def uint32) uint32 {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	if parsed, err := strconv.ParseInt(val, 10, 32); err == nil {
		if parsed < 0 || parsed > math.MaxUint32 {
			return def
		}

		return uint32(parsed)
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	if parsed, err := time.ParseDuration(val); err == nil {
		return parsed
	}
	return def
}
