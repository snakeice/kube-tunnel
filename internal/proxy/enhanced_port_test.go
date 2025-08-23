package proxy_test

import (
	"testing"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/proxy"
)

func TestUniversalPortHandler(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			VirtualInterfaceIP: "10.8.0.1",
		},
	}

	handler := proxy.NewUniversalPortHandler(
		"10.8.0.1",  // virtualIP
		"127.0.0.1", // mainProxyIP
		8080,        // mainProxyPort
		nil,         // cache
		cfg,
	)

	if handler == nil {
		t.Fatal("NewUniversalPortHandler returned nil")
	}
}
