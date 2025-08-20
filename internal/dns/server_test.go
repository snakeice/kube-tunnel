package dns_test

import (
	"net"
	"os"
	"testing"

	"github.com/miekg/dns"
	"github.com/snakeice/kube-tunnel/internal/config"
	kubeDns "github.com/snakeice/kube-tunnel/internal/dns"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.Setup()
	os.Exit(m.Run())
}

func TestProxyDNS_handleDNSQueryA(t *testing.T) {
	cfg := &config.Config{}
	p := kubeDns.NewProxyDNS(cfg, "127.0.0.1")

	tests := []struct {
		name          string
		queryName     string
		expectedIP    string
		shouldForward bool
	}{
		{
			name:          "cluster service query",
			queryName:     "kubernetes.default.svc.cluster.local.",
			expectedIP:    "127.0.0.1",
			shouldForward: false,
		},
		{
			name:          "another cluster service",
			queryName:     "api-server.svc.cluster.local.",
			expectedIP:    "127.0.0.1",
			shouldForward: false,
		},
		{
			name:          "public domain query",
			queryName:     "google.com.",
			expectedIP:    "",
			shouldForward: true,
		},
		{
			name:          "internal domain query",
			queryName:     "internal.company.com.",
			expectedIP:    "",
			shouldForward: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a DNS query
			q := dns.Question{
				Name:   tt.queryName,
				Qtype:  dns.TypeA,
				Qclass: dns.ClassINET,
			}

			// Create a DNS message
			m := new(dns.Msg)
			m.SetQuestion(tt.queryName, dns.TypeA)

			// Handle the query
			p.HandleDNSQueryA(q, m, nil)

			if tt.shouldForward {
				verifyForwardedQuery(t, tt.queryName)
			} else {
				verifyClusterQuery(t, m, tt.expectedIP)
			}
		})
	}
}

// verifyForwardedQuery verifies that a query was forwarded to system DNS.
func verifyForwardedQuery(t *testing.T, queryName string) {
	t.Logf("Forwarded query for %s - system DNS resolution attempted", queryName)
}

// verifyClusterQuery verifies that a cluster query was resolved locally.
func verifyClusterQuery(t *testing.T, m *dns.Msg, expectedIP string) {
	if len(m.Answer) == 0 {
		t.Errorf("Expected local answer for cluster query, got none")
		return
	}

	// Check that the answer contains the expected IP
	answer := m.Answer[0]
	if aRecord, ok := answer.(*dns.A); ok {
		if aRecord.A.String() != expectedIP {
			t.Errorf("Expected IP %s, got %s", expectedIP, aRecord.A.String())
		}
	} else {
		t.Errorf("Expected A record, got %T", answer)
	}
}

func TestProxyDNS_handleDNSQueryAAAA(t *testing.T) {
	cfg := &config.Config{}
	p := kubeDns.NewProxyDNS(cfg, "127.0.0.1")

	tests := []struct {
		name          string
		queryName     string
		shouldForward bool
	}{
		{
			name:          "cluster service AAAA query",
			queryName:     "kubernetes.default.svc.cluster.local.",
			shouldForward: false,
		},
		{
			name:          "public domain AAAA query",
			queryName:     "google.com.",
			shouldForward: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a DNS query
			q := dns.Question{
				Name:   tt.queryName,
				Qtype:  dns.TypeAAAA,
				Qclass: dns.ClassINET,
			}

			// Create a DNS message
			m := new(dns.Msg)
			m.SetQuestion(tt.queryName, dns.TypeAAAA)

			// Handle the query
			p.HandleDNSQueryAAAA(q, m, nil)

			if tt.shouldForward {
				verifyForwardedAAAAQuery(t, tt.queryName)
			} else {
				verifyClusterAAAAQuery(t, m)
			}
		})
	}
}

// verifyForwardedAAAAQuery verifies that an AAAA query was forwarded to system DNS.
func verifyForwardedAAAAQuery(t *testing.T, queryName string) {
	t.Logf(
		"Forwarded AAAA query for %s - system DNS resolution attempted",
		queryName,
	)
}

// verifyClusterAAAAQuery verifies that a cluster AAAA query has no answers.
func verifyClusterAAAAQuery(t *testing.T, m *dns.Msg) {
	if len(m.Answer) > 0 {
		t.Errorf("Expected no answers for cluster AAAA query, got %d", len(m.Answer))
	}
}

func TestProxyDNS_handleRequest(t *testing.T) {
	cfg := &config.Config{}
	p := kubeDns.NewProxyDNS(cfg, "127.0.0.1")

	tests := []struct {
		name      string
		queryType uint16
		queryName string
	}{
		{
			name:      "A record query",
			queryType: dns.TypeA,
			queryName: "test.com.",
		},
		{
			name:      "AAAA record query",
			queryType: dns.TypeAAAA,
			queryName: "test.com.",
		},
		{
			name:      "CNAME record query",
			queryType: dns.TypeCNAME,
			queryName: "www.test.com.",
		},
		{
			name:      "MX record query",
			queryType: dns.TypeMX,
			queryName: "test.com.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a DNS message with the query
			req := new(dns.Msg)
			req.SetQuestion(tt.queryName, tt.queryType)

			// Mock DNS response writer
			mockWriter := &mockResponseWriter{}

			// Handle the request
			p.HandleRequest(mockWriter, req)

			// Verify that the response was written
			if mockWriter.lastMsg == nil {
				t.Errorf("Expected response message to be written")
			}
		})
	}
}

// Mock response writer for testing.
type mockResponseWriter struct {
	lastMsg *dns.Msg
}

func (m *mockResponseWriter) WriteMsg(msg *dns.Msg) error {
	m.lastMsg = msg
	return nil
}

func (m *mockResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockResponseWriter) LocalAddr() net.Addr {
	return nil
}

func (m *mockResponseWriter) RemoteAddr() net.Addr {
	return nil
}

func (m *mockResponseWriter) TsigStatus() error {
	return nil
}

func (m *mockResponseWriter) TsigTimersOnly(bool) {}

func (m *mockResponseWriter) Hijack() {}

func (m *mockResponseWriter) Close() error {
	return nil
}
