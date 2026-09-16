package health //nolint:testpackage // in-package unit tests for unexported monitor internals

import (
	"sync"
	"testing"
	"time"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// TestOnUnhealthyFiresOnceOnTransition verifies that when a service hits
// maxFailures consecutive failures, the OnUnhealthy callback fires exactly
// once (edge-triggered), and does not deadlock (the callback chain
// re-acquires cacheLock via UnregisterService).
func TestOnUnhealthyFiresOnceOnTransition(t *testing.T) {
	logger.Setup()

	hm := NewHealthMonitor(config.HealthConfig{
		Enabled:       true,
		CheckInterval: 1 * time.Second,
		Timeout:       1 * time.Second,
		MaxFailures:   2,
	})

	var mu sync.Mutex
	fired := 0
	hm.OnUnhealthy = func(serviceKey string) {
		mu.Lock()
		fired++
		mu.Unlock()
	}

	// Register a service on a port nothing listens on (connection refused).
	hm.RegisterService("test-svc.default", "127.0.0.1", 1)

	// Trigger maxFailures + 1 health checks. Each will fail (connection
	// refused on port 1). The callback should fire once at FailureCount==2.
	for range hm.maxFailures + 2 {
		hm.checkServiceHealth("test-svc.default", "127.0.0.1", 1)
	}

	// Wait for the goroutine-launched callback to complete.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		count := fired
		mu.Unlock()
		if count >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("OnUnhealthy callback never fired (got %d)", count)
		case <-time.After(10 * time.Millisecond):
		}
	}

	mu.Lock()
	final := fired
	mu.Unlock()
	if final != 1 {
		t.Fatalf("expected callback to fire exactly once, got %d", final)
	}
}
