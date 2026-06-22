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

const (
	fieldKeyService    = "service"
	fieldKeyNamespace  = "namespace"
	fieldKeyMethod     = "method"
	fieldKeyPath       = "path"
	fieldKeyPod        = "pod"
	fieldKeyLocalPort  = "local_port"
	fieldKeyRemotePort = "remote_port"
	fieldKeyLocalIP    = "local_ip"
	fieldKeyDurationMS = "duration_ms"
	fieldKeyGRPC       = "grpc"
	fieldKeyAttempt    = "attempt"
	fieldKeyError      = "error"
)

func Setup() {
	once.Do(func() {
		Log = logrus.New()
	})

	// Set log level based on environment
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch level {
	case "debug":
		Log.SetLevel(logrus.DebugLevel)
	case "info":
		Log.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		Log.SetLevel(logrus.WarnLevel)
	case fieldKeyError:
		Log.SetLevel(logrus.ErrorLevel)
	case "quiet":
		Log.SetLevel(logrus.ErrorLevel) // Only show errors in quiet mode
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
	case fieldKeyService:
		return "🎯" + toString(value)
	case fieldKeyNamespace:
		return "📁" + toString(value)
	case "protocol":
		return "🔄" + toString(value)
	case "port":
		return "🔌" + toString(value)
	case fieldKeyMethod:
		return "📞" + toString(value)
	case fieldKeyPath:
		return "🛤️ " + toString(value)
	case "status":
		return "📊" + toString(value)
	case fieldKeyError:
		return "💥" + toString(value)
	case "duration":
		return "⏱️ " + toString(value)
	case fieldKeyPod:
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
	// Only log at debug level to reduce noise during normal operation
	Log.WithFields(logrus.Fields{
		fieldKeyMethod: method,
		fieldKeyPath:   path,
		"protocol":     protocol,
		"client":       remoteAddr,
	}).Debug("📨 Incoming request")
}

func LogRouting(service, namespace string) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:   service,
		fieldKeyNamespace: namespace,
	}).Info("🎯 Routing request")
}

func LogPortForward(service, namespace, pod string, localPort, remotePort int32) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:    service,
		fieldKeyNamespace:  namespace,
		fieldKeyPod:        pod,
		fieldKeyLocalPort:  localPort,
		fieldKeyRemotePort: remotePort,
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardWithTiming(
	service, namespace, pod string,
	localPort, remotePort int,
	setupDuration time.Duration,
) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:    service,
		fieldKeyNamespace:  namespace,
		fieldKeyPod:        pod,
		fieldKeyLocalPort:  localPort,
		fieldKeyRemotePort: remotePort,
		"setup_ms":         setupDuration.Milliseconds(),
	}).Info("🔗 Creating port-forward")
}

func LogPortForwardError(key string, err error, duration time.Duration) {
	Log.WithFields(logrus.Fields{
		"session":          key,
		fieldKeyError:      err.Error(),
		fieldKeyDurationMS: duration.Milliseconds(),
	}).Error("💔 Port-forward error")
}

func LogPortForwardReuse(service, namespace string, localPort int) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:   service,
		fieldKeyNamespace: namespace,
		fieldKeyLocalPort: localPort,
	}).Info("♻️  Reusing existing port-forward")
}

func LogPortForwardExpire(key string) {
	Log.WithFields(logrus.Fields{
		"session": key,
	}).Info("⏰ Expiring idle port-forward")
}

func LogPortAllocation(service, namespace string, localIP string, localPort int, preferred bool) {
	if preferred {
		Log.WithFields(logrus.Fields{
			fieldKeyService:   service,
			fieldKeyNamespace: namespace,
			fieldKeyLocalIP:   localIP,
			fieldKeyLocalPort: localPort,
		}).Info("🎯 Using preferred port for port-forward")
	} else {
		Log.WithFields(logrus.Fields{
			fieldKeyService:   service,
			fieldKeyNamespace: namespace,
			fieldKeyLocalIP:   localIP,
			fieldKeyLocalPort: localPort,
		}).Info("🔍 Allocated free port for port-forward")
	}
}

func LogPortForwardStarting(
	service, namespace, pod string,
	localIP string,
	localPort, remotePort int,
) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:    service,
		fieldKeyNamespace:  namespace,
		fieldKeyPod:        pod,
		fieldKeyLocalIP:    localIP,
		fieldKeyLocalPort:  localPort,
		fieldKeyRemotePort: remotePort,
	}).Info("🚀 Starting port-forward tunnel")
}

func LogPortForwardReady(
	service, namespace string,
	localIP string,
	localPort int,
	duration time.Duration,
) {
	Log.WithFields(logrus.Fields{
		fieldKeyService:   service,
		fieldKeyNamespace: namespace,
		fieldKeyLocalIP:   localIP,
		fieldKeyLocalPort: localPort,
		"setup_ms":        duration.Milliseconds(),
	}).Info("✅ Port-forward tunnel ready")
}

// Remove LogProtocolDetection - this is too verbose for normal operation

func LogProxy(method, path, sourceProto, targetProto string, isGRPC bool) {
	// Only log proxy operations at debug level to reduce noise
	var icon string
	if isGRPC {
		icon = "🚀"
	} else {
		icon = "📡"
	}

	Log.WithFields(logrus.Fields{
		fieldKeyMethod: method,
		fieldKeyPath:   path,
		"source_proto": sourceProto,
		"target_proto": targetProto,
		fieldKeyGRPC:   isGRPC,
	}).Debug(icon + " Proxying request")
}

func LogProxyError(method, path string, err error) {
	Log.WithFields(logrus.Fields{
		fieldKeyMethod: method,
		fieldKeyPath:   path,
		fieldKeyError:  err.Error(),
	}).Error("💥 Proxy error")
}

// Remove LogHealthCheck - health checks should be silent unless there's an issue

func LogError(message string, err error) {
	Log.WithFields(logrus.Fields{
		fieldKeyError: err.Error(),
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
		level = logrus.DebugLevel // Successful responses at debug level
	case statusCode >= 300 && statusCode < 400:
		emoji = "↗️ "
		level = logrus.DebugLevel // Redirects at debug level
	case statusCode >= 400 && statusCode < 500:
		emoji = "⚠️ "
		level = logrus.WarnLevel
	case statusCode >= 500:
		emoji = "💥"
		level = logrus.ErrorLevel
	default:
		emoji = "❓"
		level = logrus.DebugLevel
	}

	fields := logrus.Fields{
		fieldKeyMethod:     method,
		fieldKeyPath:       path,
		"status":           statusCode,
		fieldKeyDurationMS: duration.Milliseconds(),
		"response_size":    responseSize,
		fieldKeyGRPC:       isGRPC,
	}

	if duration > 5*time.Second {
		fields["performance"] = "slow"
		level = logrus.WarnLevel // Log slow requests as warnings
	}

	if statusCode >= 400 || duration > 5*time.Second {
		Log.WithFields(fields).Log(level, emoji+" Response completed")
	}
}

func LogRequestStart(method, path string, isGRPC bool, requestSize int64) {
	// Only log at debug level to reduce noise
	Log.WithFields(logrus.Fields{
		fieldKeyMethod: method,
		fieldKeyPath:   path,
		fieldKeyGRPC:   isGRPC,
		"request_size": requestSize,
	}).Debug("🚀 Request started")
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
		level = logrus.DebugLevel
	} else {
		emoji = "💔"
		level = logrus.ErrorLevel
	}

	Log.WithFields(logrus.Fields{
		fieldKeyService:    service,
		fieldKeyNamespace:  namespace,
		fieldKeyLocalPort:  localPort,
		fieldKeyDurationMS: duration.Milliseconds(),
		"success":          success,
	}).Log(level, emoji+" Proxy operation completed")
}

func LogRetry(attempt int, delay string) {
	Log.WithFields(logrus.Fields{
		fieldKeyAttempt: attempt,
		"delay":         delay,
	}).Warn("🔄 Retrying connection")
}

// Remove LogRetrySuccessWithTiming - consolidate into LogRetrySuccess

func LogRetryFailed(totalAttempts int, err error) {
	Log.WithFields(logrus.Fields{
		"total_attempts": totalAttempts,
		fieldKeyError:    err.Error(),
	}).Error("🔴 Connection failed after all retries")
}

func LogConnectionCanceled(method, path string, attempt int) {
	Log.WithFields(logrus.Fields{
		fieldKeyMethod:  method,
		fieldKeyPath:    path,
		fieldKeyAttempt: attempt,
	}).Info("🚫 Request canceled during retry")
}

func LogNonRetryableError(method, path string, err error, isGRPC bool) {
	Log.WithFields(logrus.Fields{
		fieldKeyMethod: method,
		fieldKeyPath:   path,
		fieldKeyError:  err.Error(),
		fieldKeyGRPC:   isGRPC,
	}).Error("❌ Non-retryable error")
}

func LogRetryAttempt(attempt, maxRetries int, method, path string, isGRPC bool) {
	Log.WithFields(logrus.Fields{
		fieldKeyAttempt: attempt,
		"max_retries":   maxRetries,
		fieldKeyMethod:  method,
		fieldKeyPath:    path,
		fieldKeyGRPC:    isGRPC,
	}).Debug("🎯 Retry attempt")
}
