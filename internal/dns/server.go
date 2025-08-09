package dns

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/miekg/dns"
	"github.com/snakeice/kube-tunnel/internal/logger"
	"github.com/snakeice/kube-tunnel/internal/tools"
)

type ProxyDNS struct {
	server  *dns.Server
	port    int
	quit    chan struct{}
	running bool
}

func NewProxyDNS() *ProxyDNS {
	port, err := tools.GetFreePort()
	if err != nil {
		log.Fatalf("Failed to get free port: %v", err)
	}

	return &ProxyDNS{
		port: port,
		server: &dns.Server{
			Addr: fmt.Sprintf("127.0.0.1:%d", port),
			Net:  "udp",
		},
		quit: make(chan struct{}),
	}
}

func (p *ProxyDNS) Start() error {
	if p.running {
		return errors.New("already running")
	}

	dns.HandleFunc(".", p.handleRequest)

	go func() {
		logger.Log.Infof("DNS listener iniciado em 127.0.0.1:%d", p.port)
		err := p.server.ListenAndServe()
		if err != nil {
			logger.Log.Errorf("Erro ao iniciar DNS: %v", err)
		}
	}()

	p.running = true

	if err := SetupDNS("svc.cluster.local", p.port); err != nil {
		if stopErr := p.Stop(); stopErr != nil {
			logger.LogError("Failed to stop DNS after setup failure", stopErr)
		}
		return fmt.Errorf("failed to setup DNS: %w", err)
	}

	return nil
}

func (p *ProxyDNS) Stop() error {
	if !p.running {
		return errors.New("não está rodando")
	}
	close(p.quit)
	p.running = false

	if err := RevertDNS(); err != nil {
		return fmt.Errorf("erro ao reverter DNS: %w", err)
	}

	return p.server.Shutdown()
}

func (p *ProxyDNS) handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)

	for _, q := range r.Question {
		if q.Qtype == dns.TypeA {
			p.handleDNSQueryA(q, m, r)
		}
	}

	if err := w.WriteMsg(m); err != nil {
		logger.Log.Warn("Failed to write DNS response", err.Error())
	}
}

func (*ProxyDNS) handleDNSQueryA(q dns.Question, m *dns.Msg, r *dns.Msg) {
	name := strings.ToLower(q.Name)

	if strings.HasSuffix(name, ".svc.cluster.local.") {
		rr, err := dns.NewRR(name + " 60 IN A 127.0.0.1")
		if err != nil {
			logger.Log.Warn("Failed to create DNS A record:", err.Error())
		}
		m.Answer = append(m.Answer, rr)
	} else {
		// Redirecionar para DNS externo
		upstream := "8.8.8.8:53"
		c := new(dns.Client)
		resp, _, err := c.Exchange(r, upstream)
		if err == nil && resp != nil {
			m.Answer = append(m.Answer, resp.Answer...)
		}
	}
}
