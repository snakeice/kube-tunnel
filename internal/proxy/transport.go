package proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"

	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

func createTransport(
	cfg config.PerformanceConfig,
	isHTTPS bool,
	protocol string,
	targetIP string,
	targetPort int,
) http.RoundTripper {
	dialer := func(network, addr string) (net.Conn, error) {
		var targetAddr string
		if strings.Contains(targetIP, ":") {
			targetAddr = fmt.Sprintf("[%s]:%d", targetIP, targetPort)
		} else {
			targetAddr = fmt.Sprintf("%s:%d", targetIP, targetPort)
		}
		logger.LogDebug("Connecting to target", logrus.Fields{
			"original_addr": addr,
			"target_addr":   targetAddr,
			"protocol":      protocol,
		})

		connTimeout := min(cfg.ResponseHeaderTimeout, 10*time.Second)
		conn, err := net.DialTimeout(network, targetAddr, connTimeout)
		if err != nil {
			logger.LogDebug("Connection failed", logrus.Fields{
				"target_addr": targetAddr,
				"protocol":    protocol,
				"error":       err.Error(),
			})
			return nil, fmt.Errorf("failed to connect to %s: %w", targetAddr, err)
		}

		if tcpConn, ok := conn.(*net.TCPConn); ok {
			if err := tcpConn.SetKeepAlive(true); err != nil {
				logger.LogDebug("Failed to set keep-alive", logrus.Fields{
					"error": err.Error(),
				})
			}
			if err := tcpConn.SetKeepAlivePeriod(30 * time.Second); err != nil {
				logger.LogDebug("Failed to set keep-alive period", logrus.Fields{
					"error": err.Error(),
				})
			}
		}

		logger.LogDebug("Connection established", logrus.Fields{
			"target_addr": targetAddr,
			"protocol":    protocol,
			"local_addr":  conn.LocalAddr().String(),
		})

		return conn, nil
	}

	if isHTTPS {
		return createHTTPSTransport(dialer, cfg)
	}
	return createHTTPTransport(dialer, cfg)
}

func createHTTPTransport(
	dialer func(string, string) (net.Conn, error),
	cfg config.PerformanceConfig,
) http.RoundTripper {
	transport := &http.Transport{
		Dial:                  dialer,
		DisableKeepAlives:     false,
		DisableCompression:    false,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       300 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		WriteBufferSize:       128 * 1024,
		ReadBufferSize:        128 * 1024,
		ForceAttemptHTTP2:     true,
	}

	if err := http2.ConfigureTransport(transport); err != nil {
		logger.LogError("Failed to configure HTTP/2 transport", err)
	}

	return transport
}

func createHTTPSTransport(
	dialer func(string, string) (net.Conn, error),
	cfg config.PerformanceConfig,
) http.RoundTripper {
	tlsConfig := loadTLSConfig()
	transport := &http.Transport{
		Dial:                  dialer,
		TLSClientConfig:       tlsConfig,
		DisableKeepAlives:     false,
		DisableCompression:    false,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       300 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		WriteBufferSize:       128 * 1024,
		ReadBufferSize:        128 * 1024,
		ForceAttemptHTTP2:     true,
	}

	if err := http2.ConfigureTransport(transport); err != nil {
		logger.LogError("Failed to configure HTTP/2 transport", err)
	}

	return transport
}

func createSimpleTransport(targetIP string, targetPort int) http.RoundTripper {
	dialer := func(network, addr string) (net.Conn, error) {
		var targetAddr string
		if strings.Contains(targetIP, ":") {
			targetAddr = fmt.Sprintf("[%s]:%d", targetIP, targetPort)
		} else {
			targetAddr = fmt.Sprintf("%s:%d", targetIP, targetPort)
		}
		return net.DialTimeout(network, targetAddr, 1*time.Second)
	}

	return &http.Transport{
		Dial:                dialer,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true,
	}
}

func loadTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
		NextProtos: []string{"h2", "http/1.1"},
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
		},
		PreferServerCipherSuites: true,
	}
}
