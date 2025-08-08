package config

import (
	"os"
	"strconv"
	"time"
)

// Performance configuration from environment variables.
type PerformanceConfig struct {
	SkipHealthCheck         bool
	ForceHTTP2              bool
	DisableProtocolFallback bool
	MaxIdleConns            int
	MaxIdleConnsPerHost     int
	MaxConnsPerHost         int
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxConcurrentStreams    uint32
	MaxFrameSize            uint32
	GRPCTimeout             string
}

var Config = loadPerformanceConfig()

func loadPerformanceConfig() PerformanceConfig {
	config := PerformanceConfig{
		// Defaults
		SkipHealthCheck:         false,
		ForceHTTP2:              true,
		DisableProtocolFallback: false,
		MaxIdleConns:            200,
		MaxIdleConnsPerHost:     50,
		MaxConnsPerHost:         100,
		ReadTimeout:             30 * time.Second,
		WriteTimeout:            30 * time.Second,
		IdleTimeout:             120 * time.Second,
		MaxConcurrentStreams:    1000,
		MaxFrameSize:            1048576,
		GRPCTimeout:             "30S",
	}

	config.SkipHealthCheck = parseEnvBool("SKIP_HEALTH_CHECK", config.SkipHealthCheck)
	config.ForceHTTP2 = !parseEnvBool("FORCE_HTTP2", !config.ForceHTTP2)
	config.DisableProtocolFallback = parseEnvBool(
		"DISABLE_PROTOCOL_FALLBACK",
		config.DisableProtocolFallback,
	)
	config.MaxIdleConns = parseEnvInt("MAX_IDLE_CONNS", config.MaxIdleConns)
	config.MaxIdleConnsPerHost = parseEnvInt("MAX_IDLE_CONNS_PER_HOST", config.MaxIdleConnsPerHost)
	config.MaxConnsPerHost = parseEnvInt("MAX_CONNS_PER_HOST", config.MaxConnsPerHost)
	config.ReadTimeout = parseEnvDuration("READ_TIMEOUT", config.ReadTimeout)
	config.WriteTimeout = parseEnvDuration("WRITE_TIMEOUT", config.WriteTimeout)
	config.IdleTimeout = parseEnvDuration("IDLE_TIMEOUT", config.IdleTimeout)
	config.MaxConcurrentStreams = parseEnvUint32(
		"MAX_CONCURRENT_STREAMS",
		config.MaxConcurrentStreams,
	)
	config.MaxFrameSize = parseEnvUint32("MAX_FRAME_SIZE", config.MaxFrameSize)
	if val := os.Getenv("GRPC_TIMEOUT"); val != "" {
		config.GRPCTimeout = val
	}

	return config
}

// Helper functions for environment variable parsing

func parseEnvBool(envKey string, defaultVal bool) bool {
	val := os.Getenv(envKey)
	if val == "" {
		return defaultVal
	}
	return val == "true"
}

func parseEnvInt(envKey string, defaultVal int) int {
	val := os.Getenv(envKey)
	if val == "" {
		return defaultVal
	}
	if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
		return parsed
	}
	return defaultVal
}

func parseEnvDuration(envKey string, defaultVal time.Duration) time.Duration {
	val := os.Getenv(envKey)
	if val == "" {
		return defaultVal
	}
	if parsed, err := time.ParseDuration(val); err == nil {
		return parsed
	}
	return defaultVal
}

func parseEnvUint32(envKey string, defaultVal uint32) uint32 {
	val := os.Getenv(envKey)
	if val == "" {
		return defaultVal
	}
	if parsed, err := strconv.ParseUint(val, 10, 32); err == nil {
		return uint32(parsed)
	}
	return defaultVal
}
