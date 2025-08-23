package tools_test

import (
	"testing"

	"github.com/snakeice/kube-tunnel/internal/tools"
)

func TestIsPortAvailableOnIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		port int
		// We can't easily test actual availability without system dependencies,
		// so we test the parameter validation
		expectValidCall bool
	}{
		{
			name:            "Valid IP and port",
			ip:              "127.0.0.1",
			port:            8080,
			expectValidCall: true,
		},
		{
			name:            "Invalid port - zero",
			ip:              "127.0.0.1",
			port:            0,
			expectValidCall: false,
		},
		{
			name:            "Invalid port - negative",
			ip:              "127.0.0.1",
			port:            -1,
			expectValidCall: false,
		},
		{
			name:            "Invalid port - too high",
			ip:              "127.0.0.1",
			port:            65536,
			expectValidCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tools.IsPortAvailableOnIP(tt.ip, tt.port)

			if !tt.expectValidCall {
				// For invalid parameters, should return false
				if result {
					t.Errorf(
						"IsPortAvailableOnIP(%s, %d) = true, want false for invalid parameters",
						tt.ip,
						tt.port,
					)
				}
			}
			// For valid parameters, we don't assert the result since it depends on system state
			// The test mainly ensures the function doesn't panic and handles edge cases
		})
	}
}

func TestGetFreePortOnIP(t *testing.T) {
	// Test with localhost - should work on most systems
	port, err := tools.GetFreePortOnIP("127.0.0.1")
	if err != nil {
		t.Fatalf("GetFreePortOnIP(127.0.0.1) failed: %v", err)
	}

	if port <= 0 || port > 65535 {
		t.Errorf("GetFreePortOnIP(127.0.0.1) = %d, want port in range 1-65535", port)
	}

	// Test with invalid IP - should fail
	_, err = tools.GetFreePortOnIP("invalid-ip")
	if err == nil {
		t.Error("GetFreePortOnIP(invalid-ip) should have failed")
	}
}
