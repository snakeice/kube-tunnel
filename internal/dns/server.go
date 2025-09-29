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

const (
	localhostIP = "127.0.0.1"
	tcpSuffix   = "+TCP"
)

type ProxyDNS struct {
	udpServer        *dns.Server
	tcpServer        *dns.Server
	port             int
	quit             chan struct{}
	running          bool
	resolveToIP      string
	config           *config.Config
	systemResolver   *net.Resolver
	initialBindIP    string
	interfaceManager *InterfaceManager
}

func NewProxyDNS(cfg *config.Config, portForwardIP string) *ProxyDNS {
	dnsBindIP := cfg.Network.DNSBindIP
	if dnsBindIP == "" {
		dnsBindIP = localhostIP
	}

	// If DNS bind IP is the same as virtual interface IP, start on localhost first
	// and rebind later to avoid chicken-and-egg problem
	initialBindIP := dnsBindIP
	if dnsBindIP == cfg.Network.VirtualInterfaceIP {
		initialBindIP = localhostIP
		logger.Log.Infof("DNS bind IP matches virtual interface IP, starting on localhost first")
	}

	port, err := tools.GetFreePortOnIP(initialBindIP)
	if err != nil {
		log.Fatalf("Failed to get free port on %s: %v", initialBindIP, err)
	}

	resolveIP := portForwardIP
	if resolveIP == "" {
		resolveIP = localhostIP
	}

	systemResolver := &net.Resolver{
		PreferGo: false,
	}

	udpServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", initialBindIP, port),
		Net:  "udp",
	}

	var tcpServer *dns.Server
	if cfg.Network.DNSEnableTCP {
		tcpServer = &dns.Server{
			Addr: fmt.Sprintf("%s:%d", initialBindIP, port),
			Net:  "tcp",
		}
	}

	return &ProxyDNS{
		port:           port,
		udpServer:      udpServer,
		tcpServer:      tcpServer,
		quit:           make(chan struct{}),
		resolveToIP:    resolveIP,
		config:         cfg,
		systemResolver: systemResolver,
		initialBindIP:  initialBindIP,
	}
}

func (p *ProxyDNS) Start() error {
	if p.running {
		return errors.New("already running")
	}

	dns.HandleFunc(".", p.HandleRequest)

	logger.Log.Infof(
		"DNS server starting on %s:%d, resolving *.svc.cluster.local to %s, forwarding other queries to system DNS",
		p.initialBindIP,
		p.port,
		p.resolveToIP,
	)

	if err := p.startServers(p.initialBindIP, false); err != nil {
		return fmt.Errorf("DNS server failed to start: %w", err)
	}

	logger.Log.Infof("DNS servers successfully started on %s:%d (UDP%s)",
		p.initialBindIP, p.port, p.tcpEnabledSuffix())

	p.running = true

	interfaceManager, err := SetupDNS("svc.cluster.local", p.port, p.config)
	if err != nil {
		if stopErr := p.Stop(); stopErr != nil {
			logger.LogError("Failed to stop DNS after setup failure", stopErr)
		}
		return fmt.Errorf("failed to setup DNS: %w", err)
	}

	p.interfaceManager = interfaceManager
	return nil
}

// RebindToVirtualInterface rebinds the DNS servers to the virtual interface IP.
func (p *ProxyDNS) RebindToVirtualInterface(virtualInterfaceIP string) error {
	if !p.running {
		return errors.New("DNS server is not running")
	}

	if virtualInterfaceIP == "" {
		return errors.New("virtual interface IP is required")
	}

	// Shutdown existing servers
	p.shutdownServers()

	// Create and assign new servers
	p.udpServer, p.tcpServer = p.createServers(virtualInterfaceIP)

	// Start the new servers
	if err := p.startServers(virtualInterfaceIP, true); err != nil {
		logger.Log.Warnf(
			"Failed to rebind to virtual interface, falling back to %s: %v",
			p.initialBindIP,
			err,
		)
		return p.fallbackToOriginalIP()
	}

	logger.Log.Infof("DNS servers successfully rebound to virtual interface %s:%d (UDP%s)",
		virtualInterfaceIP, p.port, p.tcpEnabledSuffix())

	return nil
}

// fallbackToOriginalIP restarts the DNS servers on the original bind IP.
func (p *ProxyDNS) fallbackToOriginalIP() error {
	// Attempt to shutdown current servers gracefully
	p.shutdownServers()

	// Create and assign new servers for original IP
	p.udpServer, p.tcpServer = p.createServers(p.initialBindIP)

	// Start servers on original IP
	if err := p.startServers(p.initialBindIP, false); err != nil {
		return fmt.Errorf("failed to fallback to original IP: %w", err)
	}

	logger.Log.Infof("DNS servers successfully fell back to %s:%d (UDP%s)",
		p.initialBindIP, p.port, p.tcpEnabledSuffix())

	return nil
}

func (p *ProxyDNS) Stop() error {
	if !p.running {
		return errors.New("DNS server is not running")
	}
	close(p.quit)
	p.running = false

	if p.interfaceManager != nil {
		logger.Log.Info("Cleaning up virtual interfaces...")
		if err := p.interfaceManager.Cleanup(); err != nil {
			logger.Log.Warnf("Failed to cleanup virtual interfaces: %v", err)
		} else {
			logger.Log.Info("Virtual interfaces cleaned up successfully")
		}
		p.interfaceManager = nil
	}

	if err := RevertDNS(); err != nil {
		return fmt.Errorf("failed to revert DNS: %w", err)
	}

	// Shutdown both UDP and TCP servers
	var udpErr, tcpErr error

	if p.udpServer != nil {
		udpErr = p.udpServer.Shutdown()
		if udpErr != nil {
			logger.Log.Warnf("Failed to shutdown UDP DNS server: %v", udpErr)
		}
	}

	if p.tcpServer != nil {
		tcpErr = p.tcpServer.Shutdown()
		if tcpErr != nil {
			logger.Log.Warnf("Failed to shutdown TCP DNS server: %v", tcpErr)
		}
	}

	// Return first error encountered, if any
	if udpErr != nil {
		return fmt.Errorf("failed to shutdown UDP server: %w", udpErr)
	}
	if tcpErr != nil {
		return fmt.Errorf("failed to shutdown TCP server: %w", tcpErr)
	}

	return nil
}

// GetVirtualInterfaceIP returns the IP of the DNS virtual interface if one was created.
func (p *ProxyDNS) GetVirtualInterfaceIP() string {
	if p.interfaceManager != nil {
		dnsInterface := p.interfaceManager.GetDNSInterface()
		if dnsInterface != nil {
			return dnsInterface.GetIP()
		}
	}
	return ""
}

// GetPortForwardIP returns the IP to use for port forwarding.
func (p *ProxyDNS) GetPortForwardIP() string {
	if p.interfaceManager != nil {
		return p.interfaceManager.GetPortForwardIP()
	}
	return "127.0.0.1"
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

// tcpEnabledSuffix returns "+TCP" if TCP is enabled, empty string otherwise.
func (p *ProxyDNS) tcpEnabledSuffix() string {
	if p.tcpServer != nil {
		return tcpSuffix
	}
	return ""
}

// startServers starts both UDP and TCP servers with error handling.
func (p *ProxyDNS) startServers(bindIP string, isRebind bool) error {
	errChan := make(chan error, 2)
	serversCount := 1

	if p.tcpServer != nil {
		serversCount = 2
	}

	// Start UDP server
	go p.startUDPServer(bindIP, errChan, isRebind)

	// Start TCP server if enabled
	if p.tcpServer != nil {
		go p.startTCPServer(bindIP, errChan, isRebind)
	}

	// Wait for servers to start
	return p.waitForServersToStart(serversCount, errChan)
}

// startUDPServer starts the UDP server.
func (p *ProxyDNS) startUDPServer(bindIP string, errChan chan<- error, isRebind bool) {
	action := "starting"
	if isRebind {
		action = "rebinding"
	}

	logger.Log.Infof("DNS UDP server %s on %s:%d", action, bindIP, p.port)

	if err := p.udpServer.ListenAndServe(); err != nil {
		logger.Log.Errorf("DNS UDP server %s failed: %v", action, err)
		errChan <- fmt.Errorf("UDP server %s failed: %w", action, err)
	} else {
		errChan <- nil
	}
}

// startTCPServer starts the TCP server.
func (p *ProxyDNS) startTCPServer(bindIP string, errChan chan<- error, isRebind bool) {
	action := "starting"
	if isRebind {
		action = "rebinding"
	}

	logger.Log.Infof("DNS TCP server %s on %s:%d", action, bindIP, p.port)

	if err := p.tcpServer.ListenAndServe(); err != nil {
		logger.Log.Errorf("DNS TCP server %s failed: %v", action, err)
		errChan <- fmt.Errorf("TCP server %s failed: %w", action, err)
	} else {
		errChan <- nil
	}
}

// waitForServersToStart waits for servers to start and returns first error if any.
func (p *ProxyDNS) waitForServersToStart(serverCount int, errChan <-chan error) error {
	for range serverCount {
		select {
		case err := <-errChan:
			if err != nil {
				return err
			}
		case <-time.After(1 * time.Second):
			// Server appears to be running
		}
	}
	return nil
}

// shutdownServers gracefully shuts down both UDP and TCP servers.
func (p *ProxyDNS) shutdownServers() {
	if p.udpServer != nil {
		if err := p.udpServer.Shutdown(); err != nil {
			logger.Log.Warnf("Failed to shutdown UDP server: %v", err)
		}
	}
	if p.tcpServer != nil {
		if err := p.tcpServer.Shutdown(); err != nil {
			logger.Log.Warnf("Failed to shutdown TCP server: %v", err)
		}
	}
}

// createServers creates new UDP and TCP server instances.
func (p *ProxyDNS) createServers(bindIP string) (*dns.Server, *dns.Server) {
	udpServer := &dns.Server{
		Addr: fmt.Sprintf("%s:%d", bindIP, p.port),
		Net:  "udp",
	}

	var tcpServer *dns.Server
	if p.config.Network.DNSEnableTCP {
		tcpServer = &dns.Server{
			Addr: fmt.Sprintf("%s:%d", bindIP, p.port),
			Net:  "tcp",
		}
	}

	return udpServer, tcpServer
}
