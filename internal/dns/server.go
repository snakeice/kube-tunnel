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
	server           *dns.Server
	port             int
	quit             chan struct{}
	running          bool
	resolveToIP      string
	config           *config.Config
	systemResolver   *net.Resolver
	initialBindIP    string
	virtualInterface *VirtualInterface
}

func NewProxyDNS(cfg *config.Config, portForwardIP string) *ProxyDNS {
	dnsBindIP := "127.0.0.1"

	port, err := tools.GetFreePortOnIP(dnsBindIP)
	if err != nil {
		log.Fatalf("Failed to get free port on localhost: %v", err)
	}

	resolveIP := portForwardIP
	if resolveIP == "" {
		resolveIP = "127.0.0.1"
	}

	systemResolver := &net.Resolver{
		PreferGo: false,
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

	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("DNS server failed to start: %w", err)
		}
		logger.Log.Infof("DNS server successfully started on %s", p.server.Addr)
	case <-time.After(1 * time.Second):
		logger.Log.Infof("DNS server appears to be running on %s", p.server.Addr)
	}

	p.running = true

	virtualInterface, err := SetupDNS("svc.cluster.local", p.port, p.config)
	if err != nil {
		if stopErr := p.Stop(); stopErr != nil {
			logger.LogError("Failed to stop DNS after setup failure", stopErr)
		}
		return fmt.Errorf("failed to setup DNS: %w", err)
	}

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

	if err := p.server.Shutdown(); err != nil {
		return fmt.Errorf("failed to shutdown current DNS server: %w", err)
	}

	newServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", virtualInterfaceIP, p.port),
		Net:  "udp",
	}

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

	select {
	case err := <-errChan:
		if err != nil {
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
		logger.Log.Infof(
			"DNS server appears to be running on virtual interface %s:%d",
			virtualInterfaceIP,
			p.port,
		)
	}

	p.server = newServer
	return nil
}

// fallbackToOriginalIP restarts the DNS server on the original bind IP.
func (p *ProxyDNS) fallbackToOriginalIP() error {
	if err := p.server.Shutdown(); err != nil {
		logger.Log.Warnf("Failed to shutdown server during fallback: %v", err)
	}

	newServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", p.initialBindIP, p.port),
		Net:  "udp",
	}

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

	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("failed to fallback to original IP: %w", err)
		}
		logger.Log.Infof("DNS server successfully fell back to %s:%d", p.initialBindIP, p.port)
	case <-time.After(1 * time.Second):
		logger.Log.Infof(
			"DNS server appears to be running on fallback IP %s:%d",
			p.initialBindIP,
			p.port,
		)
	}

	p.server = newServer
	return nil
}

func (p *ProxyDNS) Stop() error {
	if !p.running {
		return errors.New("DNS server is not running")
	}
	close(p.quit)
	p.running = false

	if p.virtualInterface != nil {
		logger.Log.Info("Cleaning up virtual interface...")
		if err := p.virtualInterface.Cleanup(); err != nil {
			logger.Log.Warnf("Failed to cleanup virtual interface: %v", err)
		} else {
			logger.Log.Info("Virtual interface cleaned up successfully")
		}
		p.virtualInterface = nil
	}

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
			p.forwardToSystemDNS(q, m, r)
		default:
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
		rr, err := dns.NewRR(fmt.Sprintf("%s 60 IN A %s", name, p.resolveToIP))
		if err != nil {
			logger.Log.Warn("Failed to create DNS A record:", err.Error())
		} else {
			m.Answer = append(m.Answer, rr)
			logger.Log.Debugf("DNS resolved %s to %s", name, p.resolveToIP)
		}
	} else {
		p.forwardToSystemDNS(q, m, r)
	}
}

func (p *ProxyDNS) HandleDNSQueryAAAA(q dns.Question, m *dns.Msg, r *dns.Msg) {
	name := strings.ToLower(q.Name)

	if strings.HasSuffix(name, ".svc.cluster.local.") {
		logger.Log.Debugf("DNS AAAA query for cluster service %s - no IPv6 support", name)
	} else {
		p.forwardToSystemDNS(q, m, r)
	}
}

// forwardToSystemDNS forwards DNS queries to the system's default resolver.
func (p *ProxyDNS) forwardToSystemDNS(q dns.Question, m *dns.Msg, _ *dns.Msg) {
	name := strings.TrimSuffix(q.Name, ".")

	logger.Log.Debugf("Forwarding DNS query %s to system resolver", name)

	ips, err := p.resolveIPs(q.Qtype, name)
	if err != nil {
		logger.Log.Debugf("System DNS resolution failed for %s: %v", name, err)
		m.Rcode = dns.RcodeNameError
		return
	}

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
		recordType = "A"
	}

	rr, err := dns.NewRR(fmt.Sprintf("%s 60 IN %s %s", q.Name, recordType, ip.String()))
	if err != nil {
		logger.Log.Warnf("Failed to create %s record for %s: %v", recordType, name, err)
		return
	}
	m.Answer = append(m.Answer, rr)
}
