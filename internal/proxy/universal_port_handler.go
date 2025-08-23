package proxy

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/snakeice/kube-tunnel/internal/cache"
	"github.com/snakeice/kube-tunnel/internal/config"
	"github.com/snakeice/kube-tunnel/internal/logger"
)

// UniversalPortHandler handles all ports on the virtual interface using iptables rules.
type UniversalPortHandler struct {
	virtualIP     string
	mainProxyIP   string
	mainProxyPort int
	cache         cache.Cache
	config        *config.Config
	mu            sync.RWMutex
	running       bool
	setupRules    [][]string // Track iptables rules for cleanup
}

// NewUniversalPortHandler creates a new universal port handler.
func NewUniversalPortHandler(
	virtualIP string,
	mainProxyIP string,
	mainProxyPort int,
	cache cache.Cache,
	cfg *config.Config,
) *UniversalPortHandler {
	return &UniversalPortHandler{
		virtualIP:     virtualIP,
		mainProxyIP:   mainProxyIP,
		mainProxyPort: mainProxyPort,
		cache:         cache,
		config:        cfg,
		setupRules:    make([][]string, 0),
	}
}

// Start sets up iptables rules to redirect all traffic on virtual interface to main proxy.
func (uph *UniversalPortHandler) Start() error {
	uph.mu.Lock()
	defer uph.mu.Unlock()

	if uph.running {
		return errors.New("universal port handler is already running")
	}

	// Set up iptables rules to redirect all traffic on virtual interface to main proxy
	rules := [][]string{
		// Redirect all TCP traffic destined for virtual interface to main proxy
		{
			"sudo", "iptables", "-t", "nat", "-A", "OUTPUT",
			"-d", uph.virtualIP,
			"-p", "tcp",
			"--dport", "1:65535",
			"-j", "DNAT",
			"--to-destination", fmt.Sprintf("%s:%d", uph.mainProxyIP, uph.mainProxyPort),
		},
		// Ensure proper masquerading for the redirected traffic
		{
			"sudo", "iptables", "-t", "nat", "-A", "POSTROUTING",
			"-d", uph.mainProxyIP,
			"-p", "tcp",
			"--dport", strconv.Itoa(uph.mainProxyPort),
			"-j", "MASQUERADE",
		},
	}

	for _, rule := range rules {
		logger.Log.Debugf("Setting up iptables rule: %s", strings.Join(rule, " "))
		out, err := exec.Command(rule[0], rule[1:]...).CombinedOutput()
		if err != nil {
			// Clean up any rules that were successfully added
			uph.cleanupRules()
			return fmt.Errorf("failed to setup iptables rule: %w, output: %s", err, string(out))
		}
		logger.Log.Debugf("iptables rule added successfully: %s", string(out))

		// Store rule for cleanup (convert ADD to DELETE)
		cleanupRule := make([]string, len(rule))
		copy(cleanupRule, rule)
		for i, arg := range cleanupRule {
			if arg == "-A" {
				cleanupRule[i] = "-D"
				break
			}
		}
		uph.setupRules = append(uph.setupRules, cleanupRule)
	}

	uph.running = true
	logger.Log.Infof("🔀 Universal port handler active: %s:* -> %s:%d",
		uph.virtualIP, uph.mainProxyIP, uph.mainProxyPort)

	return nil
}

// Stop removes the iptables rules.
func (uph *UniversalPortHandler) Stop() error {
	uph.mu.Lock()
	defer uph.mu.Unlock()

	if !uph.running {
		return nil
	}

	uph.cleanupRules()
	uph.running = false
	logger.Log.Info("Universal port handler stopped")

	return nil
}

// cleanupRules removes all iptables rules that were added.
func (uph *UniversalPortHandler) cleanupRules() {
	for _, rule := range uph.setupRules {
		logger.Log.Debugf("Removing iptables rule: %s", strings.Join(rule, " "))
		out, err := exec.Command(rule[0], rule[1:]...).CombinedOutput()
		if err != nil {
			logger.Log.Warnf("Failed to remove iptables rule: %v, output: %s", err, string(out))
		} else {
			logger.Log.Debugf("iptables rule removed: %s", string(out))
		}
	}
	uph.setupRules = make([][]string, 0)
}

// EnhancedPortManager combines the original port manager with universal port handling.
type EnhancedPortManager struct {
	*PortManager
	universalHandler *UniversalPortHandler
	cache            cache.Cache
	config           *config.Config
}

// NewEnhancedPortManager creates a new enhanced port manager.
func NewEnhancedPortManager(
	virtualIP string,
	mainProxyIP string,
	mainProxyPort int,
	cache cache.Cache,
	cfg *config.Config,
	proxyHandler *Proxy,
) *EnhancedPortManager {
	originalPM := NewPortManager(mainProxyIP, mainProxyPort, cfg, proxyHandler)
	universalHandler := NewUniversalPortHandler(virtualIP, mainProxyIP, mainProxyPort, cache, cfg)

	return &EnhancedPortManager{
		PortManager:      originalPM,
		universalHandler: universalHandler,
		cache:            cache,
		config:           cfg,
	}
}

// StartUniversalPortHandling starts the universal port handling for the virtual interface.
func (epm *EnhancedPortManager) StartUniversalPortHandling() error {
	return epm.universalHandler.Start()
}

// StopUniversalPortHandling stops the universal port handling.
func (epm *EnhancedPortManager) StopUniversalPortHandling() error {
	return epm.universalHandler.Stop()
}

// StartWithUniversalHandling starts both the original port manager and universal handling.
func (epm *EnhancedPortManager) StartWithUniversalHandling() error {
	// Start universal port handling first
	if err := epm.StartUniversalPortHandling(); err != nil {
		return fmt.Errorf("failed to start universal port handling: %w", err)
	}

	logger.Log.Info("Enhanced port manager started with universal port handling")
	return nil
}

// UpdateMainProxyPort updates the main proxy port for the universal handler.
func (epm *EnhancedPortManager) UpdateMainProxyPort(port int) {
	epm.universalHandler.mainProxyPort = port
}

// StopAll stops all port handling.
func (epm *EnhancedPortManager) StopAll() error {
	var errors []string

	// Stop universal handling
	if err := epm.StopUniversalPortHandling(); err != nil {
		errors = append(errors, fmt.Sprintf("universal handling: %v", err))
	}

	// Stop original port manager
	if err := epm.PortManager.StopAll(); err != nil {
		errors = append(errors, fmt.Sprintf("port manager: %v", err))
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors during shutdown: %s", strings.Join(errors, "; "))
	}

	logger.Log.Info("Enhanced port manager stopped completely")
	return nil
}
