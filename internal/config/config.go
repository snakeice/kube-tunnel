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
	dummyInterfaceType = "dummy"
)

type Config struct {
	Performance PerformanceConfig
	Health      HealthConfig
	Network     NetworkConfig
	Proxy       ProxyConfig
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

type ProxyConfig struct {
	MaxRetries int
	RetryDelay time.Duration
}

func GetConfig() *Config {
	perf := createDefaultPerformanceConfig()
	health := createDefaultHealthConfig()
	network := createDefaultNetworkConfig()
	proxy := createDefaultProxyConfig()

	// Apply environment overrides
	perf = applyPerformanceOverrides(perf)
	health = applyHealthOverrides(health)
	network = applyNetworkOverrides(network)
	proxy = applyProxyOverrides(proxy)

	// Validate configurations
	perf = validatePerformanceConfig(perf)
	health = validateHealthConfig(health)
	network = validateNetworkConfig(network)
	proxy = validateProxyConfig(proxy)

	return &Config{
		Performance: perf,
		Health:      health,
		Network:     network,
		Proxy:       proxy,
	}
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
		VirtualInterfaceIP:   "127.0.0.10",
		InterfaceType:        dummyInterfaceType,
		DNSBindIP:            "127.0.0.1",
		PortForwardBindIP:    "127.0.0.1",
		CustomIPRanges: []string{
			"127.0.0.0/24",
			"127.1.0.0/16",
		},
	}
}

// createDefaultProxyConfig creates the default proxy configuration.
func createDefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		MaxRetries: 2,
		RetryDelay: 100 * time.Millisecond,
	}
}

// applyPerformanceOverrides applies environment variable overrides to performance config.
func applyPerformanceOverrides(perf PerformanceConfig) PerformanceConfig {
	perf.SkipHealthCheck = getEnvBool("KTUN_SKIP_HEALTH", perf.SkipHealthCheck)
	perf.ForceHTTP2 = getEnvBool("KTUN_FORCE_HTTP2", perf.ForceHTTP2)
	perf.DisableProtocolFallback = getEnvBool("KTUN_DISABLE_FALLBACK", perf.DisableProtocolFallback)
	perf.MaxIdleConns = getEnvInt("KTUN_MAX_IDLE", perf.MaxIdleConns)
	perf.MaxIdleConnsPerHost = getEnvInt("KTUN_MAX_IDLE_HOST", perf.MaxIdleConnsPerHost)
	perf.MaxConnsPerHost = getEnvInt("KTUN_MAX_CONNS_HOST", perf.MaxConnsPerHost)
	perf.ReadTimeout = getEnvDuration("KTUN_READ_TIMEOUT", perf.ReadTimeout)
	perf.WriteTimeout = getEnvDuration("KTUN_WRITE_TIMEOUT", perf.WriteTimeout)
	perf.IdleTimeout = getEnvDuration("KTUN_IDLE_TIMEOUT", perf.IdleTimeout)
	perf.MaxConcurrentStreams = getEnvUint32("KTUN_MAX_STREAMS", perf.MaxConcurrentStreams)
	perf.MaxFrameSize = getEnvUint32("KTUN_MAX_FRAME", perf.MaxFrameSize)
	perf.MaxUploadBufferPerConnection = getEnvInt32(
		"KTUN_MAX_UPLOAD_CONN",
		perf.MaxUploadBufferPerConnection,
	)
	perf.MaxUploadBufferPerStream = getEnvInt32(
		"KTUN_MAX_UPLOAD_STREAM",
		perf.MaxUploadBufferPerStream,
	)

	if val := os.Getenv("KTUN_GRPC_TIMEOUT"); val != "" {
		perf.GRPCTimeout = val
	}
	return perf
}

// applyHealthOverrides applies environment variable overrides to health config.
func applyHealthOverrides(health HealthConfig) HealthConfig {
	health.Enabled = getEnvBool("KTUN_HEALTH_ENABLED", health.Enabled)
	health.CheckInterval = getEnvDuration("KTUN_HEALTH_INTERVAL", health.CheckInterval)
	health.Timeout = getEnvDuration("KTUN_HEALTH_TIMEOUT", health.Timeout)
	health.MaxFailures = getEnvInt("KTUN_HEALTH_MAX_FAIL", health.MaxFailures)
	return health
}

// applyNetworkOverrides applies environment variable overrides to network config.
func applyNetworkOverrides(network NetworkConfig) NetworkConfig {
	if val := os.Getenv("KTUN_DNS_IP"); val != "" {
		network.DNSBindIP = val
	}
	if val := os.Getenv("KTUN_FORWARD_IP"); val != "" {
		network.PortForwardBindIP = val
	}
	network.UseVirtualInterface = getEnvBool("KTUN_USE_VIRTUAL", network.UseVirtualInterface)
	if val := os.Getenv("KTUN_VIRTUAL_NAME"); val != "" {
		network.VirtualInterfaceName = val
	}
	if val := os.Getenv("KTUN_VIRTUAL_IP"); val != "" {
		network.VirtualInterfaceIP = val
	}

	// Interface type validation - only allow dummy
	if val := os.Getenv("KTUN_INTERFACE_TYPE"); val != "" {
		if val == dummyInterfaceType {
			network.InterfaceType = val
		} else {
			logger.Log.Warnf("Invalid interface type '%s', using default '%s'", val, dummyInterfaceType)
		}
	}

	// Custom IP ranges for finding free IPs
	if val := os.Getenv("KTUN_IP_RANGES"); val != "" {
		ranges := strings.Split(val, ",")
		for i, rangeStr := range ranges {
			ranges[i] = strings.TrimSpace(rangeStr)
		}
		network.CustomIPRanges = ranges
	}
	return network
}

// applyProxyOverrides applies environment variable overrides to proxy config.
func applyProxyOverrides(proxy ProxyConfig) ProxyConfig {
	proxy.MaxRetries = getEnvInt("KTUN_RETRY_MAX", proxy.MaxRetries)
	proxy.RetryDelay = getEnvDuration("KTUN_RETRY_DELAY", proxy.RetryDelay)
	return proxy
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

// validateProxyConfig validates proxy configuration values.
func validateProxyConfig(proxy ProxyConfig) ProxyConfig {
	if proxy.MaxRetries < 0 {
		proxy.MaxRetries = 0
	}
	if proxy.MaxRetries > 10 {
		proxy.MaxRetries = 10
	}
	if proxy.RetryDelay < 25*time.Millisecond {
		proxy.RetryDelay = 25 * time.Millisecond
	}
	if proxy.RetryDelay > 2*time.Second {
		proxy.RetryDelay = 2 * time.Second
	}
	return proxy
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
