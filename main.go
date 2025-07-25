package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Parse command-line flags
	var (
		port   = flag.Int("port", 80, "Port to run the proxy server on")
		noMDNS = flag.Bool("no-mdns", false, "Disable mDNS server")
		help   = flag.Bool("help", false, "Show help message")
	)
	flag.Parse()

	if *help {
		flag.Usage()
		printUsageExamples()
		return
	}

	LogStartup("Starting kube-tunnel proxy server on port " + fmt.Sprintf("%d", *port))

	// Initialize health monitor after logger is set up
	InitializeHealthMonitor()

	// Start zeroconf server for *.svc.cluster.local resolution if not disabled
	var zeroconfServer *ZeroconfServer
	if !*noMDNS {
		zeroconfServer = NewZeroconfServer()
		if err := zeroconfServer.SafeStart(); err != nil {
			LogError("Failed to start zeroconf server", err)
			log.Warn("🌐 Continuing without zeroconf - manual DNS configuration required")
		}
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create the main handler
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthCheckHandler)
	mux.HandleFunc("/health/status", healthStatusHandler)
	mux.HandleFunc("/health/metrics", healthMetricsHandler)
	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		servicesHandler(w, r, zeroconfServer)
	})
	mux.HandleFunc("/", proxyHandler)

	// Configure HTTP/2 server with performance config settings
	h2Server := &http2.Server{
		MaxConcurrentStreams:         perfConfig.MaxConcurrentStreams,
		MaxReadFrameSize:             perfConfig.MaxFrameSize,
		PermitProhibitedCipherSuites: false,
		IdleTimeout:                  perfConfig.IdleTimeout,
		MaxUploadBufferPerConnection: int32(perfConfig.MaxFrameSize),
		MaxUploadBufferPerStream:     int32(perfConfig.MaxFrameSize),
	}

	// Create h2c handler for HTTP/2 over cleartext
	h2cHandler := h2c.NewHandler(mux, h2Server)

	// Create HTTP server that handles all protocols automatically
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      h2cHandler, // This handles HTTP/1.1, h2c, and protocol upgrades
		ReadTimeout:  perfConfig.ReadTimeout,
		WriteTimeout: perfConfig.WriteTimeout,
		IdleTimeout:  perfConfig.IdleTimeout,
	}

	// Configure HTTP/2 support for TLS connections
	if err := http2.ConfigureServer(server, h2Server); err != nil {
		LogError("Failed to configure HTTP/2", err)
	}

	var features []string
	features = append(features, "HTTP/1.1", "h2c", "gRPC")
	if !*noMDNS {
		features = append(features, "zeroconf")
	}

	LogStartup("Ready - supporting " + strings.Join(features, ", "))
	if !*noMDNS {
		log.Info("🌐 All *.svc.cluster.local domains will resolve to this proxy")
	}
	log.Info(fmt.Sprintf("💡 Test: curl http://my-service.default.svc.cluster.local:%d/health", *port))
	log.Info(fmt.Sprintf("📊 Health monitoring: http://localhost:%d/health/status", *port))
	log.Info(fmt.Sprintf("📈 Health metrics: http://localhost:%d/health/metrics", *port))

	// Start HTTP server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithField("error", err.Error()).Fatal("Server failed")
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	LogStartup("Shutdown signal received")

	// Graceful shutdown
	if zeroconfServer != nil {
		zeroconfServer.Stop()
	}

	// Stop health monitor
	if globalHealthMonitor != nil {
		globalHealthMonitor.Stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		LogError("Server shutdown failed", err)
	}

	LogStartup("Shutdown complete")
}

func printUsageExamples() {
	fmt.Println(`
Examples:
  kube-tunnel                              Start proxy with full DNS automation
  kube-tunnel -port=8080                   Start proxy on port 8080
  kube-tunnel -no-dns                      Start proxy without DNS configuration
  kube-tunnel -no-mdns                     Start proxy without mDNS server
  kube-tunnel -dns-only                    Only configure DNS, don't start proxy
  kube-tunnel -cleanup                     Clean up DNS configuration and exit

DNS Configuration:
  The proxy automatically configures your system to resolve *.svc.cluster.local
  domains to the proxy server. This includes:

  • macOS: /etc/resolver/cluster.local
  • Linux: /etc/hosts entries
  • Windows: Manual configuration required

  Use -no-dns to disable automatic DNS configuration.
  Use -cleanup to remove DNS configuration when done.

Zeroconf/mDNS Server:
  The zeroconf server provides full service discovery and responds to
  *.svc.cluster.local DNS queries on the local network. It automatically
  discovers and tracks Kubernetes services using mDNS/DNS-SD protocols.

  Use -no-mdns to disable the zeroconf server.

Testing:
  curl http://temporal.temporal.svc.cluster.local/health
  curl http://prometheus.monitoring.svc.cluster.local:9090/metrics
  	grpcurl temporal.temporal.svc.cluster.local:80 list

  API Endpoints:
    /health                                  Health check endpoint
    /services                                List discovered services (JSON)

  `)
}
