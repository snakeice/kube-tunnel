package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/spf13/viper"
)

// safeIntToUint32 safely converts int to uint32 with bounds checking.
func safeIntToUint32(val int) uint32 {
	if val < 0 {
		return 0
	}
	// Use math.MaxInt32 as a safe upper bound that works on all platforms
	if val > math.MaxInt32 {
		return math.MaxUint32
	}
	return uint32(val)
}

// safeIntToInt32 safely converts int to int32 with bounds checking.
func safeIntToInt32(val int) int32 {
	if val < math.MinInt32 {
		return math.MinInt32
	}
	if val > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(val)
}

// InitViper initializes the viper configuration.
func InitViper(configFile string) error {
	viper.SetConfigType("yaml")

	viper.SetEnvPrefix("KTUN")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/kube-tunnel/")
	viper.AddConfigPath("$HOME/.kube-tunnel/")
	viper.AddConfigPath(".")

	setDefaults()

	if configFile != "" {
		return readConfigFromFile(configFile)
	}

	if err := viper.ReadInConfig(); err != nil {
		logger.LogDebug("No config file found in default locations", nil)
	}
	return nil
}

// readConfigFromFile reads configuration from a specific file path.
func readConfigFromFile(configFile string) error {
	if !filepath.IsAbs(configFile) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
		configFile = filepath.Join(cwd, configFile)
	}

	viper.SetConfigFile(configFile)

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	return nil
}

// setDefaults sets the default values for configuration.
func setDefaults() {
	// Performance defaults
	viper.SetDefault("performance.skipHealthCheck", false)
	viper.SetDefault("performance.forceHTTP2", true)
	viper.SetDefault("performance.disableProtocolFallback", false)
	viper.SetDefault("performance.maxIdleConns", 200)
	viper.SetDefault("performance.maxIdleConnsPerHost", 50)
	viper.SetDefault("performance.maxConnsPerHost", 100)
	viper.SetDefault("performance.readTimeout", "120s")
	viper.SetDefault("performance.writeTimeout", "120s")
	viper.SetDefault("performance.idleTimeout", "120s")
	viper.SetDefault("performance.responseHeaderTimeout", "30s")
	viper.SetDefault("performance.proxyTimeout", "60s")
	viper.SetDefault("performance.maxConcurrentStreams", 1000)
	viper.SetDefault("performance.maxFrameSize", 512*Kb)
	viper.SetDefault("performance.grpcTimeout", "30s")
	viper.SetDefault("performance.maxUploadBufferPerConnection", 1*Mb)
	viper.SetDefault("performance.maxUploadBufferPerStream", 1*Mb)

	// Health defaults
	viper.SetDefault("health.enabled", true)
	viper.SetDefault("health.checkInterval", "30s")
	viper.SetDefault("health.timeout", "2s")
	viper.SetDefault("health.maxFailures", 3)

	// Network defaults
	viper.SetDefault("network.useVirtualInterface", true)
	viper.SetDefault("network.virtualInterfaceName", "kube-dns0")
	viper.SetDefault("network.virtualInterfaceIP", defaultDNSIP)
	viper.SetDefault("network.portForwardInterfaceName", "kube-proxy0")
	viper.SetDefault("network.portForwardInterfaceIP", defaultPortForwardIP)
	viper.SetDefault("network.dnsBindIP", defaultDNSIP)
	viper.SetDefault("network.portForwardBindIP", defaultPortForwardIP)
	viper.SetDefault("network.customIPRanges", []string{
		"10.8.0.0/24",
		"10.9.0.0/24",
	})

	// Proxy defaults
	viper.SetDefault("proxy.maxRetries", 2)
	viper.SetDefault("proxy.retryDelay", "100ms")
}

// LoadConfigFromViper creates a config object from viper.
func LoadConfigFromViper() *Config {
	perf := PerformanceConfig{
		SkipHealthCheck:         viper.GetBool("performance.skipHealthCheck"),
		ForceHTTP2:              viper.GetBool("performance.forceHTTP2"),
		DisableProtocolFallback: viper.GetBool("performance.disableProtocolFallback"),
		MaxIdleConns:            viper.GetInt("performance.maxIdleConns"),
		MaxIdleConnsPerHost:     viper.GetInt("performance.maxIdleConnsPerHost"),
		MaxConnsPerHost:         viper.GetInt("performance.maxConnsPerHost"),
		MaxConcurrentStreams:    safeIntToUint32(viper.GetInt("performance.maxConcurrentStreams")),
		MaxFrameSize:            safeIntToUint32(viper.GetInt("performance.maxFrameSize")),
		GRPCTimeout:             viper.GetString("performance.grpcTimeout"),
	}

	// Parse durations
	if readTimeout, err := time.ParseDuration(viper.GetString("performance.readTimeout")); err == nil {
		perf.ReadTimeout = readTimeout
	}
	if writeTimeout, err := time.ParseDuration(viper.GetString("performance.writeTimeout")); err == nil {
		perf.WriteTimeout = writeTimeout
	}
	if idleTimeout, err := time.ParseDuration(viper.GetString("performance.idleTimeout")); err == nil {
		perf.IdleTimeout = idleTimeout
	}
	if responseHeaderTimeout, err := time.ParseDuration(viper.GetString("performance.responseHeaderTimeout")); err == nil {
		perf.ResponseHeaderTimeout = responseHeaderTimeout
	}
	if proxyTimeout, err := time.ParseDuration(viper.GetString("performance.proxyTimeout")); err == nil {
		perf.ProxyTimeout = proxyTimeout
	}

	// Parse buffer sizes
	perf.MaxUploadBufferPerConnection = safeIntToInt32(
		viper.GetInt("performance.maxUploadBufferPerConnection"),
	)
	perf.MaxUploadBufferPerStream = safeIntToInt32(
		viper.GetInt("performance.maxUploadBufferPerStream"),
	)

	health := HealthConfig{
		Enabled:     viper.GetBool("health.enabled"),
		MaxFailures: viper.GetInt("health.maxFailures"),
	}

	// Parse durations for health
	if checkInterval, err := time.ParseDuration(viper.GetString("health.checkInterval")); err == nil {
		health.CheckInterval = checkInterval
	}
	if timeout, err := time.ParseDuration(viper.GetString("health.timeout")); err == nil {
		health.Timeout = timeout
	}

	network := NetworkConfig{
		UseVirtualInterface:      viper.GetBool("network.useVirtualInterface"),
		VirtualInterfaceName:     viper.GetString("network.virtualInterfaceName"),
		VirtualInterfaceIP:       viper.GetString("network.virtualInterfaceIP"),
		PortForwardInterfaceName: viper.GetString("network.portForwardInterfaceName"),
		DNSBindIP:                viper.GetString("network.dnsBindIP"),
		PortForwardBindIP:        viper.GetString("network.portForwardBindIP"),
	}

	proxy := ProxyConfig{
		MaxRetries: viper.GetInt("proxy.maxRetries"),
	}

	// Parse duration for proxy
	if retryDelay, err := time.ParseDuration(viper.GetString("proxy.retryDelay")); err == nil {
		proxy.RetryDelay = retryDelay
	}

	// Validate the configurations
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
