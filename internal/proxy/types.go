package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/health"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode   int
	responseSize int64
	written      bool
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	if !w.written {
		w.statusCode = statusCode
		w.written = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterWrapper) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		return n, err
	}
	w.responseSize += int64(n)
	return n, nil
}

func (w *responseWriterWrapper) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type ProxyCacheKey struct {
	Service   string
	Namespace string
	IsGRPC    bool
}

type SetupCacheEntry struct {
	LocalIP   string
	LocalPort int
	Expiry    time.Time
}

type Proxy struct {
	cache      cache.Cache
	health     *health.Monitor
	cfg        *config.Config
	proxyCache sync.Map
	setupCache sync.Map
}
