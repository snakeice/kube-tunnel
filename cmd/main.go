package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/dns"
	"github.com/snakeice/kube-tunnel/internal/health"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/proxy"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Parse command-line flags
	var (
		port = flag.Int("port", 80, "Port to run the proxy server on")
		help = flag.Bool("help", false, "Show help message")
	)
	flag.Parse()

	if *help {
		flag.Usage()
		printUsageExamples()
		return
	}

	logger.LogStartup("Starting kube-tunnel proxy server on port " + strconv.Itoa(*port))

	// Initialize health monitor after logger is set up
	health.InitializeHealthMonitor()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create the main handler
	mux := http.NewServeMux()
	mux.HandleFunc("/", proxy.ProxyHandler)
	mux.HandleFunc("/health", proxy.HealthCheckHandler)
	mux.HandleFunc("/health/status", proxy.HealthStatusHandler)
	mux.HandleFunc("/health/metrics", proxy.HealthMetricsHandler)

	// Create HTTP/2 server with h2c support
	h2s := &http2.Server{
		MaxConcurrentStreams:         config.Config.MaxConcurrentStreams,
		MaxReadFrameSize:             config.Config.MaxFrameSize,
		PermitProhibitedCipherSuites: false,
		IdleTimeout:                  config.Config.IdleTimeout,
		MaxUploadBufferPerConnection: int32(config.Config.MaxFrameSize),
		MaxUploadBufferPerStream:     int32(config.Config.MaxFrameSize),
	}
	handler := h2c.NewHandler(mux, h2s)

	server := &http.Server{
		Addr:         ":" + strconv.Itoa(*port),
		Handler:      handler,
		ReadTimeout:  config.Config.ReadTimeout,
		WriteTimeout: config.Config.WriteTimeout,
		IdleTimeout:  config.Config.IdleTimeout,
	}

	// Enable HTTP/2
	if err := http2.ConfigureServer(server, h2s); err != nil {
		logger.LogError("Failed to configure HTTP/2 server", err)
		os.Exit(1)
	}

	dnsServer := dns.NewProxyDNS()
	if err := dnsServer.Start(); err != nil {
		logger.LogError("Failed to start DNS server", err)
		os.Exit(1)
	}

	// Start server in a goroutine
	go func() {
		logger.Log.Info(
			fmt.Sprintf(
				"💡 Test: curl http://my-service.default.svc.cluster.local:%d/health",
				*port,
			),
		)
		logger.Log.Info(
			fmt.Sprintf("📊 Health monitoring: http://localhost:%d/health/status", *port),
		)
		logger.Log.Info(fmt.Sprintf("📈 Health metrics: http://localhost:%d/health/metrics", *port))

		logger.Log.Infof("🚀 Server listening on port %d (HTTP/1.1, h2c, h2 with TLS, gRPC)", *port)
		logger.Log.Info("✅ Ready to proxy requests to *.svc.cluster.local services")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.LogError("Server failed to start", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	logger.Log.Info("🛑 Received shutdown signal, gracefully shutting down...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.LogError("Server forced to shutdown", err)
	}

	if err := dnsServer.Stop(); err != nil {
		logger.LogError("Failed to stop DNS server", err)
	}

	logger.Log.Info("✅ Server stopped gracefully")
}

func printUsageExamples() {
	examples := `
Examples:

  # Start proxy server on port 80
  ./kube-tunnel

  # Start on custom port
  ./kube-tunnel -port=8080

  # Enable debug logging
  LOG_LEVEL=debug ./kube-tunnel

After starting, you can make requests like:
  curl http://my-service.default.svc.cluster.local/health
  curl --http2-prior-knowledge http://grpc-service.default.svc.cluster.local/api
`
	fmt.Print(examples)
}
