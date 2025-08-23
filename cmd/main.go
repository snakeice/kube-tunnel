package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/snakeice/kube-tunnel/internal/app"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	var (
		port      = flag.Int("port", 80, "Port to run the proxy server on")
		help      = flag.Bool("help", false, "Show help message")
		verbose   = flag.Bool("verbose", false, "Enable verbose/debug logging")
		quiet     = flag.Bool("quiet", false, "Enable quiet mode (errors only)")
		virtual   = flag.Bool("virtual", false, "Enable virtual interface mode")
		virtualIP = flag.String(
			"virtual-ip",
			"",
			"Virtual interface IP address (default: auto-allocate)",
		)
		healthCheck = flag.Bool("health", true, "Enable health monitoring")
		dnsIP       = flag.String("dns-ip", "127.0.0.1", "DNS server bind IP address")
		maxRetries  = flag.Int("max-retries", 2, "Maximum retry attempts for requests")
	)
	flag.Parse()

	// Apply flag overrides to environment before logger setup
	if *verbose {
		os.Setenv("LOG_LEVEL", "debug")
	}
	if *quiet {
		os.Setenv("LOG_LEVEL", "quiet")
	}
	if *virtual {
		os.Setenv("KTUN_USE_VIRTUAL", "true")
	}
	if *virtualIP != "" {
		os.Setenv("KTUN_VIRTUAL_IP", *virtualIP)
	}
	if !*healthCheck {
		os.Setenv("KTUN_HEALTH_ENABLED", "false")
	}
	if *dnsIP != "127.0.0.1" {
		os.Setenv("KTUN_DNS_IP", *dnsIP)
	}
	if *maxRetries != 2 {
		os.Setenv("KTUN_RETRY_MAX", strconv.Itoa(*maxRetries))
	}

	logger.Setup()
	if *help {
		flag.Usage()
		printUsageExamples()
		return
	}
	logger.LogStartup("Starting kube-tunnel proxy server on port " + strconv.Itoa(*port))
	container, err := app.Build()
	if err != nil {
		logger.LogError("Failed to build application container", err)
		os.Exit(1)
	}
	conf := container.Cfg
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	mux := container.Mux

	server := createServer(*port, conf, mux)
	go startServer(server, *port, container)

	// Start enhanced port manager if available
	if err := container.StartPortManager(*port); err != nil {
		logger.LogError("Failed to start enhanced port manager", err)
		// Continue without failing - basic functionality will still work
	}

	<-sigChan
	shutdownServer(server, container)
}

// createServer creates and configures the HTTP server.
func createServer(port int, conf *config.Config, mux http.Handler) *http.Server {
	h2s := &http2.Server{
		MaxConcurrentStreams:         conf.Performance.MaxConcurrentStreams,
		MaxReadFrameSize:             conf.Performance.MaxFrameSize,
		PermitProhibitedCipherSuites: false,
		IdleTimeout:                  conf.Performance.IdleTimeout,
		MaxUploadBufferPerConnection: conf.Performance.MaxUploadBufferPerConnection,
		MaxUploadBufferPerStream:     conf.Performance.MaxUploadBufferPerStream,
	}
	handler := h2c.NewHandler(mux, h2s)

	addr := ":" + strconv.Itoa(port)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  conf.Performance.ReadTimeout,
		WriteTimeout: conf.Performance.WriteTimeout,
		IdleTimeout:  conf.Performance.IdleTimeout,
	}

	if err := http2.ConfigureServer(server, h2s); err != nil {
		logger.LogError("Failed to configure HTTP/2 server", err)
		os.Exit(1)
	}

	return server
}

// startServer starts the server and logs essential startup information.
func startServer(server *http.Server, port int, container *app.Container) {
	displayHost := "localhost"
	url := "http://" + displayHost + ":" + strconv.Itoa(port)

	logger.Log.Infof("🌐 Proxy server running at %s", url)
	logger.Log.Infof("🔗 Port forwards using IP: %s", container.Cache.GetPortForwardIP())
	logger.Log.Infof("📱 Dashboard: %s/dashboard", url)
	logger.Log.Infof("🚀 Server listening on %s (HTTP/1.1, h2c, h2 with TLS, gRPC)", server.Addr)
	logger.Log.Info("✅ Ready to proxy requests to *.svc.cluster.local services")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.LogError("Server failed to start", err)
		os.Exit(1)
	}
}

// shutdownServer gracefully shuts down the server and application.
func shutdownServer(server *http.Server, container *app.Container) {
	logger.Log.Info("🛑 Received shutdown signal, gracefully shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.LogError("Server forced to shutdown", err)
	}
	if err := container.Shutdown(ctx); err != nil {
		logger.LogError("Failed to shutdown application cleanly", err)
	}
	logger.Log.Info("✅ Server stopped gracefully")
}

func printUsageExamples() {
	examples := `
Examples:

  # Start proxy server on default port 80
  ./kube-tunnel

  # Start on custom port with debug logging
  ./kube-tunnel -port=8080 -verbose

  # Enable virtual interface mode
  ./kube-tunnel -virtual -virtual-ip=10.8.0.1

  # Disable health monitoring for development
  ./kube-tunnel -health=false -verbose

  # Custom DNS configuration
  ./kube-tunnel -dns-ip=127.0.0.1 -max-retries=3

  # Full configuration example
  ./kube-tunnel -port=8080 -virtual -virtual-ip=192.168.1.100 -verbose

Environment variables (KTUN_* prefix) take precedence over flags.
See ENV_VARS.md for complete configuration options.

After starting, you can make requests like:
  curl http://my-service.default.svc.cluster.local/health
  curl --http2-prior-knowledge http://grpc-service.default.svc.cluster.local/api
`
	logger.Log.Info(examples)
}
