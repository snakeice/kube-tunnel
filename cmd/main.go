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
	"github.com/snakeice/kube-tunnel/internal/logger"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	var (
		port = flag.Int("port", 80, "Port to run the proxy server on")
		help = flag.Bool("help", false, "Show help message")
	)
	flag.Parse()
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

	h2s := &http2.Server{
		MaxConcurrentStreams:         conf.Performance.MaxConcurrentStreams,
		MaxReadFrameSize:             conf.Performance.MaxFrameSize,
		PermitProhibitedCipherSuites: false,
		IdleTimeout:                  conf.Performance.IdleTimeout,
		MaxUploadBufferPerConnection: conf.Performance.MaxUploadBufferPerConnection,
		MaxUploadBufferPerStream:     conf.Performance.MaxUploadBufferPerStream,
	}
	handler := h2c.NewHandler(mux, h2s)
	server := &http.Server{
		Addr:         ":" + strconv.Itoa(*port),
		Handler:      handler,
		ReadTimeout:  conf.Performance.ReadTimeout,
		WriteTimeout: conf.Performance.WriteTimeout,
		IdleTimeout:  conf.Performance.IdleTimeout,
	}
	if err := http2.ConfigureServer(server, h2s); err != nil {
		logger.LogError("Failed to configure HTTP/2 server", err)
		os.Exit(1)
	}
	go func() {
		url := "http://localhost:" + strconv.Itoa(*port)
		logger.Log.Infof("🌐 Proxy server running at %s", url)

		logger.Log.Infof(
			"💡 Test: curl http://my-service.default.svc.cluster.local:%d/health", *port)
		logger.Log.Infof("📊 Health monitoring: %s/health/status", url)
		logger.Log.Infof("📈 Health metrics: %s/health/metrics", url)
		logger.Log.Infof("🚀 Server listening on port %d (HTTP/1.1, h2c, h2 with TLS, gRPC)", *port)
		logger.Log.Info("✅ Ready to proxy requests to *.svc.cluster.local services")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.LogError("Server failed to start", err)
			os.Exit(1)
		}
	}()
	<-sigChan
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
	logger.Log.Info(examples)
}
