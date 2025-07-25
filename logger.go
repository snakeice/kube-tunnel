package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var log *logrus.Logger

func init() {
	log = logrus.New()

	// Set output to stdout
	log.SetOutput(os.Stdout)

	// Set log level based on environment
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch level {
	case "debug":
		log.SetLevel(logrus.DebugLevel)
	case "info":
		log.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		log.SetLevel(logrus.WarnLevel)
	case "error":
		log.SetLevel(logrus.ErrorLevel)
	default:
		log.SetLevel(logrus.InfoLevel)
	}

	// Use custom formatter with colors and emojis
	log.SetFormatter(&CustomFormatter{})
}

type CustomFormatter struct{}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var emoji string
	var color string
	var tag string

	// Reset color
	reset := "\033[0m"

	switch entry.Level {
	case logrus.DebugLevel:
		emoji = "🔍"
		color = "\033[36m" // Cyan
		tag = "DEBUG"
	case logrus.InfoLevel:
		emoji = "ℹ️ "
		color = "\033[32m" // Green
		tag = "INFO"
	case logrus.WarnLevel:
		emoji = "⚠️ "
		color = "\033[33m" // Yellow
		tag = "WARN"
	case logrus.ErrorLevel:
		emoji = "❌"
		color = "\033[31m" // Red
		tag = "ERROR"
	case logrus.FatalLevel:
		emoji = "💀"
		color = "\033[35m" // Magenta
		tag = "FATAL"
	default:
		emoji = "📝"
		color = "\033[37m" // White
		tag = "LOG"
	}

	// Format: [EMOJI] [COLOR][TAG][RESET] message [fields]
	message := entry.Message

	// Add fields if present
	if len(entry.Data) > 0 {
		var fields []string
		for k, v := range entry.Data {
			fields = append(fields, formatField(k, v))
		}
		message += " " + strings.Join(fields, " ")
	}

	formatted := emoji + " " + color + "[" + tag + "]" + reset + " " + message + "\n"
	return []byte(formatted), nil
}

func formatField(key string, value any) string {
	switch key {
	case "service":
		return "🎯" + toString(value)
	case "namespace":
		return "📁" + toString(value)
	case "protocol":
		return "🔄" + toString(value)
	case "port":
		return "🔌" + toString(value)
	case "method":
		return "📞" + toString(value)
	case "path":
		return "🛤️ " + toString(value)
	case "status":
		return "📊" + toString(value)
	case "error":
		return "💥" + toString(value)
	case "duration":
		return "⏱️ " + toString(value)
	case "pod":
		return "🐋" + toString(value)
	default:
		return key + "=" + toString(value)
	}
}

func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case int32:
		return strconv.Itoa(int(val))
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Convenience functions with emojis and structured fields.
func LogStartup(message string) {
	log.WithFields(logrus.Fields{
		"component": "startup",
	}).Info("🚀 " + message)
}

func LogRequest(method, path, protocol, remoteAddr string) {
	log.WithFields(logrus.Fields{
		"method":   method,
		"path":     path,
		"protocol": protocol,
		"client":   remoteAddr,
	}).Info("📨 Incoming request")
}

func LogRouting(service, namespace string) {
	log.WithFields(logrus.Fields{
		"service":   service,
		"namespace": namespace,
	}).Info("🎯 Routing request")
}

func LogPortForward(service, namespace, pod string, localPort, remotePort int32) {
	log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"pod":         pod,
		"local_port":  localPort,
		"remote_port": remotePort,
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardWithTiming(
	service, namespace, pod string,
	localPort, remotePort int32,
	setupDuration time.Duration,
) {
	log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"pod":         pod,
		"local_port":  localPort,
		"remote_port": remotePort,
		"setup_ms":    setupDuration.Milliseconds(),
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardError(key string, err error, duration time.Duration) {
	log.WithFields(logrus.Fields{
		"session":     key,
		"error":       err.Error(),
		"duration_ms": duration.Milliseconds(),
	}).Error("💔 Port-forward error")
}

func LogPortForwardReuse(service, namespace string, localPort int32) {
	log.WithFields(logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"local_port": localPort,
	}).Debug("♻️  Reusing port-forward")
}

func LogPortForwardExpire(key string) {
	log.WithFields(logrus.Fields{
		"session": key,
	}).Info("⏰ Expiring idle port-forward")
}

func LogProtocolDetection(target, protocol string) {
	log.WithFields(logrus.Fields{
		"target":   target,
		"protocol": protocol,
	}).Debug("🔍 Detected backend protocol")
}

func LogProxy(method, path, sourceProto, targetProto string, isGRPC bool) {
	var icon string
	if isGRPC {
		icon = "🚀"
	} else {
		icon = "📡"
	}

	log.WithFields(logrus.Fields{
		"method":       method,
		"path":         path,
		"source_proto": sourceProto,
		"target_proto": targetProto,
		"grpc":         isGRPC,
	}).Info(icon + " Proxying request")
}

func LogProxyError(method, path string, err error) {
	log.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"error":  err.Error(),
	}).Error("💥 Proxy error")
}

func LogHealthCheck(protocol string) {
	log.WithFields(logrus.Fields{
		"protocol": protocol,
	}).Debug("❤️  Health check")
}

func LogError(message string, err error) {
	log.WithFields(logrus.Fields{
		"error": err.Error(),
	}).Error("❌ " + message)
}

func LogDebug(message string, fields logrus.Fields) {
	log.WithFields(fields).Debug("🔧 " + message)
}

func LogResponseMetrics(
	method, path string,
	statusCode int,
	duration time.Duration,
	responseSize int64,
	isGRPC bool,
) {
	var emoji string
	var level logrus.Level

	// Choose emoji and level based on status code
	if statusCode >= 200 && statusCode < 300 {
		emoji = "✅"
		level = logrus.InfoLevel
	} else if statusCode >= 300 && statusCode < 400 {
		emoji = "↗️ "
		level = logrus.InfoLevel
	} else if statusCode >= 400 && statusCode < 500 {
		emoji = "⚠️ "
		level = logrus.WarnLevel
	} else if statusCode >= 500 {
		emoji = "💥"
		level = logrus.ErrorLevel
	} else {
		emoji = "❓"
		level = logrus.InfoLevel
	}

	fields := logrus.Fields{
		"method":        method,
		"path":          path,
		"status":        statusCode,
		"duration_ms":   duration.Milliseconds(),
		"response_size": responseSize,
		"grpc":          isGRPC,
	}

	// Add performance indicators
	if duration > 5*time.Second {
		fields["performance"] = "slow"
	} else if duration < 100*time.Millisecond {
		fields["performance"] = "fast"
	}

	log.WithFields(fields).Log(level, emoji+" Response completed")
}

func LogRequestStart(method, path string, isGRPC bool, requestSize int64) {
	log.WithFields(logrus.Fields{
		"method":       method,
		"path":         path,
		"grpc":         isGRPC,
		"request_size": requestSize,
	}).Info("🚀 Request started")
}

func LogProxyMetrics(
	service, namespace string,
	localPort int32,
	duration time.Duration,
	success bool,
) {
	var emoji string
	var level logrus.Level

	if success {
		emoji = "🎯"
		level = logrus.InfoLevel
	} else {
		emoji = "💔"
		level = logrus.ErrorLevel
	}

	log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"local_port":  localPort,
		"duration_ms": duration.Milliseconds(),
		"success":     success,
	}).Log(level, emoji+" Proxy operation completed")
}

func LogRetry(attempt int, delay string, err error) {
	log.WithFields(logrus.Fields{
		"attempt": attempt,
		"delay":   delay,
		"error":   err.Error(),
	}).Warn("🔄 Retrying connection")
}

func LogRetryWithTiming(attempt uint, delay string, err error, attemptDuration time.Duration) {
	log.WithFields(logrus.Fields{
		"attempt":    attempt,
		"delay":      delay,
		"error":      err.Error(),
		"attempt_ms": attemptDuration.Milliseconds(),
	}).Warn("🔄 Retrying connection")
}

func LogRetrySuccess(attempt int) {
	log.WithFields(logrus.Fields{
		"attempt": attempt,
	}).Info("✅ Connection successful after retry")
}

func LogRetrySuccessWithTiming(attempt uint, attemptDuration, totalDuration time.Duration) {
	log.WithFields(logrus.Fields{
		"attempt":    attempt,
		"attempt_ms": attemptDuration.Milliseconds(),
		"total_ms":   totalDuration.Milliseconds(),
	}).Info("✅ Connection successful after retry")
}

func LogRetryFailed(totalAttempts int, err error) {
	log.WithFields(logrus.Fields{
		"total_attempts": totalAttempts,
		"error":          err.Error(),
	}).Error("🔴 Connection failed after all retries")
}

func LogRetryFailedWithTiming(totalAttempts uint, err error, totalDuration time.Duration) {
	log.WithFields(logrus.Fields{
		"total_attempts": totalAttempts,
		"error":          err.Error(),
		"total_ms":       totalDuration.Milliseconds(),
	}).Error("🔴 Connection failed after all retries")
}

func LogBackendHealth(port int32, status string) {
	log.WithFields(logrus.Fields{
		"port":   port,
		"status": status,
	}).Debug("🏥 Backend health check")
}

func LogConnectionCanceled(method, path string, attempt uint) {
	log.WithFields(logrus.Fields{
		"method":  method,
		"path":    path,
		"attempt": attempt,
	}).Info("🚫 Request canceled during retry")
}

func LogNonRetryableError(method, path string, err error, isGRPC bool) {
	log.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"error":  err.Error(),
		"grpc":   isGRPC,
	}).Error("❌ Non-retryable error")
}

func LogRetryAttempt(attempt, maxRetries uint, method, path string, isGRPC bool) {
	log.WithFields(logrus.Fields{
		"attempt":     attempt,
		"max_retries": maxRetries,
		"method":      method,
		"path":        path,
		"grpc":        isGRPC,
	}).Debug("🎯 Retry attempt")
}
