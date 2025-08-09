package logger

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	Log  *logrus.Logger
	once sync.Once
)

func Setup() {
	once.Do(func() {
		Log = logrus.New()
	})

	// Set output to stdout
	Log.SetOutput(os.Stdout)

	// Set log level based on environment
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch level {
	case "debug":
		Log.SetLevel(logrus.DebugLevel)
	case "info":
		Log.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		Log.SetLevel(logrus.WarnLevel)
	case "error":
		Log.SetLevel(logrus.ErrorLevel)
	default:
		Log.SetLevel(logrus.InfoLevel)
	}

	// Use custom formatter with colors and emojis
	Log.SetFormatter(&CustomFormatter{})
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
	case logrus.PanicLevel:
		emoji = "🚨"
		color = "\033[41m" // Red background
		tag = "PANIC"
	case logrus.TraceLevel:
		emoji = "🔎"
		color = "\033[34m" // Blue
		tag = "TRACE"
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
	Log.WithFields(logrus.Fields{
		"component": "startup",
	}).Info("🚀 " + message)
}

func LogRequest(method, path, protocol, remoteAddr string) {
	Log.WithFields(logrus.Fields{
		"method":   method,
		"path":     path,
		"protocol": protocol,
		"client":   remoteAddr,
	}).Info("📨 Incoming request")
}

func LogRouting(service, namespace string) {
	Log.WithFields(logrus.Fields{
		"service":   service,
		"namespace": namespace,
	}).Info("🎯 Routing request")
}

func LogPortForward(service, namespace, pod string, localPort, remotePort int32) {
	Log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"pod":         pod,
		"local_port":  localPort,
		"remote_port": remotePort,
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardWithTiming(
	service, namespace, pod string,
	localPort, remotePort int,
	setupDuration time.Duration,
) {
	Log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"pod":         pod,
		"local_port":  localPort,
		"remote_port": remotePort,
		"setup_ms":    setupDuration.Milliseconds(),
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardError(key string, err error, duration time.Duration) {
	Log.WithFields(logrus.Fields{
		"session":     key,
		"error":       err.Error(),
		"duration_ms": duration.Milliseconds(),
	}).Error("💔 Port-forward error")
}

func LogPortForwardReuse(service, namespace string, localPort int) {
	Log.WithFields(logrus.Fields{
		"service":    service,
		"namespace":  namespace,
		"local_port": localPort,
	}).Debug("♻️  Reusing port-forward")
}

func LogPortForwardExpire(key string) {
	Log.WithFields(logrus.Fields{
		"session": key,
	}).Info("⏰ Expiring idle port-forward")
}

func LogProtocolDetection(target, protocol string) {
	Log.WithFields(logrus.Fields{
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

	Log.WithFields(logrus.Fields{
		"method":       method,
		"path":         path,
		"source_proto": sourceProto,
		"target_proto": targetProto,
		"grpc":         isGRPC,
	}).Info(icon + " Proxying request")
}

func LogProxyError(method, path string, err error) {
	Log.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"error":  err.Error(),
	}).Error("💥 Proxy error")
}

func LogHealthCheck(protocol string) {
	Log.WithFields(logrus.Fields{
		"protocol": protocol,
	}).Debug("❤️  Health check")
}

func LogError(message string, err error) {
	Log.WithFields(logrus.Fields{
		"error": err.Error(),
	}).Error("❌ " + message)
}

func LogDebug(message string, fields logrus.Fields) {
	Log.WithFields(fields).Debug("🔧 " + message)
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
	switch {
	case statusCode >= 200 && statusCode < 300:
		emoji = "✅"
		level = logrus.InfoLevel
	case statusCode >= 300 && statusCode < 400:
		emoji = "↗️ "
		level = logrus.InfoLevel
	case statusCode >= 400 && statusCode < 500:
		emoji = "⚠️ "
		level = logrus.WarnLevel
	case statusCode >= 500:
		emoji = "💥"
		level = logrus.ErrorLevel
	default:
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

	Log.WithFields(fields).Log(level, emoji+" Response completed")
}

func LogRequestStart(method, path string, isGRPC bool, requestSize int64) {
	Log.WithFields(logrus.Fields{
		"method":       method,
		"path":         path,
		"grpc":         isGRPC,
		"request_size": requestSize,
	}).Info("🚀 Request started")
}

func LogProxyMetrics(
	service, namespace string,
	localPort int,
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

	Log.WithFields(logrus.Fields{
		"service":     service,
		"namespace":   namespace,
		"local_port":  localPort,
		"duration_ms": duration.Milliseconds(),
		"success":     success,
	}).Log(level, emoji+" Proxy operation completed")
}

func LogRetry(attempt int, delay string, err error) {
	Log.WithFields(logrus.Fields{
		"attempt": attempt,
		"delay":   delay,
		"error":   err.Error(),
	}).Warn("🔄 Retrying connection")
}

func LogRetryWithTiming(attempt int, delay string, err error, attemptDuration time.Duration) {
	Log.WithFields(logrus.Fields{
		"attempt":    attempt,
		"delay":      delay,
		"error":      err.Error(),
		"attempt_ms": attemptDuration.Milliseconds(),
	}).Warn("🔄 Retrying connection")
}

func LogRetrySuccess(attempt int) {
	Log.WithFields(logrus.Fields{
		"attempt": attempt,
	}).Info("✅ Connection successful after retry")
}

func LogRetrySuccessWithTiming(attempt int, attemptDuration, totalDuration time.Duration) {
	Log.WithFields(logrus.Fields{
		"attempt":    attempt,
		"attempt_ms": attemptDuration.Milliseconds(),
		"total_ms":   totalDuration.Milliseconds(),
	}).Info("✅ Connection successful after retry")
}

func LogRetryFailed(totalAttempts int, err error) {
	Log.WithFields(logrus.Fields{
		"total_attempts": totalAttempts,
		"error":          err.Error(),
	}).Error("🔴 Connection failed after all retries")
}

func LogRetryFailedWithTiming(totalAttempts int, err error, totalDuration time.Duration) {
	Log.WithFields(logrus.Fields{
		"total_attempts": totalAttempts,
		"error":          err.Error(),
		"total_ms":       totalDuration.Milliseconds(),
	}).Error("🔴 Connection failed after all retries")
}

func LogBackendHealth(port int, status string) {
	Log.WithFields(logrus.Fields{
		"port":   port,
		"status": status,
	}).Debug("🏥 Backend health check")
}

func LogConnectionCanceled(method, path string, attempt int) {
	Log.WithFields(logrus.Fields{
		"method":  method,
		"path":    path,
		"attempt": attempt,
	}).Info("🚫 Request canceled during retry")
}

func LogNonRetryableError(method, path string, err error, isGRPC bool) {
	Log.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"error":  err.Error(),
		"grpc":   isGRPC,
	}).Error("❌ Non-retryable error")
}

func LogRetryAttempt(attempt, maxRetries int, method, path string, isGRPC bool) {
	Log.WithFields(logrus.Fields{
		"attempt":     attempt,
		"max_retries": maxRetries,
		"method":      method,
		"path":        path,
		"grpc":        isGRPC,
	}).Debug("🎯 Retry attempt")
}
