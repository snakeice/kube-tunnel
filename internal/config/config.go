package config

import (
	"math"
	"os"
	"strconv"
	"time"
)

const (
	Kb = 1024
	Mb = 1024 * Kb
	Gb = 1024 * Mb
)

type Config struct {
	Performance PerformanceConfig
	Health      HealthConfig
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
	Enabled         bool
	CheckInterval   time.Duration
	Timeout         time.Duration
	MaxFailures     int
	RecoveryRetries int
}

func GetConfig() Config {
	perf := PerformanceConfig{
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
	health := HealthConfig{
		Enabled:         true,
		CheckInterval:   30 * time.Second,
		Timeout:         2 * time.Second,
		MaxFailures:     3,
		RecoveryRetries: 2,
	}

	// Performance overrides
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

	// Health overrides
	health.Enabled = !getEnvBool("HEALTH_MONITOR_ENABLED", !health.Enabled)
	health.CheckInterval = getEnvDuration("HEALTH_CHECK_INTERVAL", health.CheckInterval)
	health.Timeout = getEnvDuration("HEALTH_CHECK_TIMEOUT", health.Timeout)
	health.MaxFailures = getEnvInt("HEALTH_MAX_FAILURES", health.MaxFailures)
	health.RecoveryRetries = getEnvInt("HEALTH_RECOVERY_RETRIES", health.RecoveryRetries)

	// Validation
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
	if health.CheckInterval < time.Second {
		health.CheckInterval = 1 * time.Second
	}
	if health.Timeout < 100*time.Millisecond {
		health.Timeout = 100 * time.Millisecond
	}
	if health.MaxFailures < 1 {
		health.MaxFailures = 1
	}
	if health.RecoveryRetries < 0 {
		health.RecoveryRetries = 0
	}
	return Config{Performance: perf, Health: health}
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
