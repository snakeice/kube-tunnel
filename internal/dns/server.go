package dns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

type ProxyDNS struct {
	server      *dns.Server
	port        int
	quit        chan struct{}
	running     bool
	resolveToIP string
	config      *config.Config
	// Add system DNS resolver for forwarding non-cluster queries
	systemResolver *net.Resolver
	// Store the initial bind IP for potential rebinding
	initialBindIP string
	// Track the virtual interface for cleanup
	virtualInterface *VirtualInterface
}

func NewProxyDNS(cfg *config.Config, portForwardIP string) *ProxyDNS {
	// Always start DNS server on localhost first to avoid binding issues
	// The virtual interface will be created separately when DNS is configured
	dnsBindIP := "127.0.0.1"

	port, err := tools.GetFreePortOnIP(dnsBindIP)
	if err != nil {
		log.Fatalf("Failed to get free port on localhost: %v", err)
	}

	// Resolve to the same IP where port forwards listen
	resolveIP := portForwardIP
	if resolveIP == "" {
		resolveIP = "127.0.0.1"
	}

	// Create system DNS resolver for forwarding non-cluster queries
	systemResolver := &net.Resolver{
		PreferGo: false, // Use system's native resolver (systemd-resolved)
	}

	return &ProxyDNS{
		port: port,
		server: &dns.Server{
			Addr: fmt.Sprintf("%s:%d", dnsBindIP, port),
			Net:  "udp",
		},
		quit:           make(chan struct{}),
		resolveToIP:    resolveIP,
		config:         cfg,
		systemResolver: systemResolver,
		initialBindIP:  dnsBindIP,
	}
}

func (p *ProxyDNS) Start() error {
	if p.running {
		return errors.New("already running")
	}

	dns.HandleFunc(".", p.HandleRequest)

	// Channel to capture DNS server startup errors
	errChan := make(chan error, 1)

	go func() {
		logger.Log.Infof(
			"DNS server starting on localhost:%d, resolving *.svc.cluster.local to %s, forwarding other queries to system DNS",
			p.port,
			p.resolveToIP,
		)
		err := p.server.ListenAndServe()
		if err != nil {
			logger.Log.Errorf("DNS server failed: %v", err)
			errChan <- err
		} else {
			errChan <- nil
		}
	}()

	// Wait a moment to see if DNS server starts successfully
	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("DNS server failed to start: %w", err)
		}
		logger.Log.Infof("DNS server successfully started on %s", p.server.Addr)
	case <-time.After(1 * time.Second):
		// Assume it started successfully if no error in 1 second
		logger.Log.Infof("DNS server appears to be running on %s", p.server.Addr)
	}

	p.running = true

	// Setup DNS configuration on virtual interface
	virtualInterface, err := SetupDNS("svc.cluster.local", p.port, p.config)
	if err != nil {
		if stopErr := p.Stop(); stopErr != nil {
			logger.LogError("Failed to stop DNS after setup failure", stopErr)
		}
		return fmt.Errorf("failed to setup DNS: %w", err)
	}

	// Store the virtual interface reference for cleanup
	p.virtualInterface = virtualInterface

	return nil
}

// RebindToVirtualInterface rebinds the DNS server to the virtual interface IP.
func (p *ProxyDNS) RebindToVirtualInterface(virtualInterfaceIP string) error {
	if !p.running {
		return errors.New("DNS server is not running")
	}

	if virtualInterfaceIP == "" {
		return errors.New("virtual interface IP is required")
	}

	// Stop the current server
	if err := p.server.Shutdown(); err != nil {
		return fmt.Errorf("failed to shutdown current DNS server: %w", err)
	}

	// Create new server bound to virtual interface IP
	newServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", virtualInterfaceIP, p.port),
		Net:  "udp",
	}

	// Start the new server
	errChan := make(chan error, 1)
	go func() {
		logger.Log.Infof(
			"DNS server rebinding to virtual interface %s:%d",
			virtualInterfaceIP,
			p.port,
		)
		err := newServer.ListenAndServe()
		if err != nil {
			logger.Log.Errorf("DNS server rebind failed: %v", err)
			errChan <- err
		} else {
			errChan <- nil
		}
	}()

	// Wait a moment to see if the new server starts successfully
	select {
	case err := <-errChan:
		if err != nil {
			// If rebinding fails, try to restart on the original IP
			logger.Log.Warnf(
				"Failed to rebind to virtual interface, falling back to %s: %v",
				p.initialBindIP,
				err,
			)
			return p.fallbackToOriginalIP()
		}
		logger.Log.Infof(
			"DNS server successfully rebound to virtual interface %s:%d",
			virtualInterfaceIP,
			p.port,
		)
	case <-time.After(1 * time.Second):
		// Assume it started successfully if no error in 1 second
		logger.Log.Infof(
			"DNS server appears to be running on virtual interface %s:%d",
			virtualInterfaceIP,
			p.port,
		)
	}

	// Update the server reference
	p.server = newServer
	return nil
}

// fallbackToOriginalIP restarts the DNS server on the original bind IP.
func (p *ProxyDNS) fallbackToOriginalIP() error {
	// Stop the current server
	if err := p.server.Shutdown(); err != nil {
		logger.Log.Warnf("Failed to shutdown server during fallback: %v", err)
	}

	// Create new server bound to original IP
	newServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", p.initialBindIP, p.port),
		Net:  "udp",
	}

	// Start the new server
	errChan := make(chan error, 1)
	go func() {
		logger.Log.Infof("DNS server falling back to %s:%d", p.initialBindIP, p.port)
		err := newServer.ListenAndServe()
		if err != nil {
			logger.Log.Errorf("DNS server fallback failed: %v", err)
			errChan <- err
		} else {
			errChan <- nil
		}
	}()

	// Wait a moment to see if the fallback server starts successfully
	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("failed to fallback to original IP: %w", err)
		}
		logger.Log.Infof("DNS server successfully fell back to %s:%d", p.initialBindIP, p.port)
	case <-time.After(1 * time.Second):
		// Assume it started successfully if no error in 1 second
		logger.Log.Infof(
			"DNS server appears to be running on fallback IP %s:%d",
			p.initialBindIP,
			p.port,
		)
	}

	// Update the server reference
	p.server = newServer
	return nil
}

func (p *ProxyDNS) Stop() error {
	if !p.running {
		return errors.New("DNS server is not running")
	}
	close(p.quit)
	p.running = false

	// Clean up virtual interface if it was created
	if p.virtualInterface != nil {
		logger.Log.Info("Cleaning up virtual interface...")
		if err := p.virtualInterface.Cleanup(); err != nil {
			logger.Log.Warnf("Failed to cleanup virtual interface: %v", err)
		} else {
			logger.Log.Info("Virtual interface cleaned up successfully")
		}
		p.virtualInterface = nil
	}

	// Revert DNS configuration
	if err := RevertDNS(); err != nil {
		return fmt.Errorf("failed to revert DNS: %w", err)
	}

	return p.server.Shutdown()
}

// GetVirtualInterfaceIP returns the IP of the virtual interface if one was created.
func (p *ProxyDNS) GetVirtualInterfaceIP() string {
	if p.virtualInterface != nil {
		return p.virtualInterface.GetIP()
	}
	return ""
}

func (p *ProxyDNS) HandleRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)

	for _, q := range r.Question {
		switch q.Qtype {
		case dns.TypeA:
			p.HandleDNSQueryA(q, m, r)
		case dns.TypeAAAA:
			p.HandleDNSQueryAAAA(q, m, r)
		case dns.TypeCNAME, dns.TypeMX, dns.TypeTXT, dns.TypeSRV, dns.TypeNS:
			// Forward common query types to system DNS
			p.forwardToSystemDNS(q, m, r)
		default:
			// For other query types, try to forward to system DNS
			p.forwardToSystemDNS(q, m, r)
		}
	}

	if err := w.WriteMsg(m); err != nil {
		logger.Log.Warn("Failed to write DNS response", err.Error())
	}
}

func (p *ProxyDNS) HandleDNSQueryA(q dns.Question, m *dns.Msg, r *dns.Msg) {
	name := strings.ToLower(q.Name)

	if strings.HasSuffix(name, ".svc.cluster.local.") {
		// Handle cluster service queries locally
		rr, err := dns.NewRR(fmt.Sprintf("%s 60 IN A %s", name, p.resolveToIP))
		if err != nil {
			logger.Log.Warn("Failed to create DNS A record:", err.Error())
		} else {
			m.Answer = append(m.Answer, rr)
			logger.Log.Debugf("DNS resolved %s to %s", name, p.resolveToIP)
		}
	} else {
		// Forward non-cluster queries to system DNS
		p.forwardToSystemDNS(q, m, r)
	}
}

func (p *ProxyDNS) HandleDNSQueryAAAA(q dns.Question, m *dns.Msg, r *dns.Msg) {
	name := strings.ToLower(q.Name)

	if strings.HasSuffix(name, ".svc.cluster.local.") {
		// For cluster service queries, return empty response (no IPv6)
		logger.Log.Debugf("DNS AAAA query for cluster service %s - no IPv6 support", name)
	} else {
		// Forward non-cluster queries to system DNS
		p.forwardToSystemDNS(q, m, r)
	}
}

// forwardToSystemDNS forwards DNS queries to the system's default resolver.
func (p *ProxyDNS) forwardToSystemDNS(q dns.Question, m *dns.Msg, _ *dns.Msg) {
	name := strings.TrimSuffix(q.Name, ".")

	logger.Log.Debugf("Forwarding DNS query %s to system resolver", name)

	// Use the system resolver to get the answer
	ips, err := p.resolveIPs(q.Qtype, name)
	if err != nil {
		logger.Log.Debugf("System DNS resolution failed for %s: %v", name, err)
		// Set NXDOMAIN response
		m.Rcode = dns.RcodeNameError
		return
	}

	// Add the resolved IPs to the answer
	p.addResolvedIPs(q, m, ips, name)

	if len(m.Answer) > 0 {
		logger.Log.Debugf("System DNS resolved %s to %d IP(s)", name, len(m.Answer))
	} else {
		logger.Log.Debugf("System DNS returned no valid IPs for %s", name)
	}
}

// resolveIPs resolves IP addresses for the given query type and name.
func (p *ProxyDNS) resolveIPs(qtype uint16, name string) ([]net.IP, error) {
	switch qtype {
	case dns.TypeA:
		return p.systemResolver.LookupIP(context.Background(), "ip4", name)
	case dns.TypeAAAA:
		return p.systemResolver.LookupIP(context.Background(), "ip6", name)
	default:
		// For other query types, try to get any IP
		return p.systemResolver.LookupIP(context.Background(), "ip", name)
	}
}

// addResolvedIPs adds the resolved IPs to the DNS message answer.
func (p *ProxyDNS) addResolvedIPs(q dns.Question, m *dns.Msg, ips []net.IP, name string) {
	for _, ip := range ips {
		if p.isValidIPForQueryType(q.Qtype, ip) {
			p.addIPRecord(q, m, ip, name)
		}
	}
}

// isValidIPForQueryType checks if the IP is valid for the given query type.
func (p *ProxyDNS) isValidIPForQueryType(qtype uint16, ip net.IP) bool {
	switch qtype {
	case dns.TypeA:
		return ip.To4() != nil
	case dns.TypeAAAA:
		return ip.To4() == nil
	default:
		return true
	}
}

// addIPRecord adds an IP record to the DNS message answer.
func (p *ProxyDNS) addIPRecord(q dns.Question, m *dns.Msg, ip net.IP, name string) {
	var recordType string
	switch q.Qtype {
	case dns.TypeA:
		recordType = "A"
	case dns.TypeAAAA:
		recordType = "AAAA"
	default:
		recordType = "A" // Default to A record for other types
	}

	rr, err := dns.NewRR(fmt.Sprintf("%s 60 IN %s %s", q.Name, recordType, ip.String()))
	if err != nil {
		logger.Log.Warnf("Failed to create %s record for %s: %v", recordType, name, err)
		return
	}
	m.Answer = append(m.Answer, rr)
}
