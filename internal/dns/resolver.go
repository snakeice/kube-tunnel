package dns

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

var configuredInterface string

// getDefaultInterface detects active network interface (non-loopback).
func getDefaultInterface() (string, error) {
	routeCmd := exec.Command("ip", "route", "get", "8.8.8.8")
	output, err := routeCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to detect default route: %w", err)
	}

	out := string(output)
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("interface not found in output: %s", out)
}

// SetupDNS configures systemd-resolved to redirect domain to our local DNS.
func SetupDNS(domain string, port int) error {
	iface, err := getDefaultInterface()
	if err != nil {
		return err
	}
	configuredInterface = iface

	cmds := [][]string{
		{"sudo", "-v"},
		{"sudo", "resolvectl", "dns", iface, fmt.Sprintf("127.0.0.1:%d", port)},
		{"sudo", "resolvectl", "domain", iface, fmt.Sprintf("~%s", domain)},
	}

	for _, cmd := range cmds {
		logger.Log.Infof("Executing: %s", strings.Join(cmd, " "))
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			logger.Log.Errorf("Error: %v\nOutput: %s", err, out)
			return fmt.Errorf("failed to apply resolvectl: %w", err)
		}
	}

	logger.Log.Infof(
		"resolvectl configuration applied on interface %s for domain ~%s",
		iface,
		domain,
	)
	return nil
}

// RevertDNS reverts DNS configurations on the used interface.
func RevertDNS() error {
	if configuredInterface == "" {
		var err error
		configuredInterface, err = getDefaultInterface()
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("sudo", "resolvectl", "revert", configuredInterface)
	logger.Log.Infof("Executing: %s", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Errorf("Error: %v\nOutput: %s", err, out)
		return fmt.Errorf("failed to revert resolvectl: %w", err)
	}

	logger.Log.Infof("resolvectl revert executed successfully on interface %s", configuredInterface)
	return nil
}
