package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

func main() {
	fmt.Println("🔍 Zeroconf Service Discovery Example")
	fmt.Println("This example demonstrates how the new zeroconf implementation works")
	fmt.Println()

	// Example 1: Browse for all HTTP services
	fmt.Println("📡 Browsing for HTTP services...")
	browseServices("_http._tcp", "local.")

	// Example 2: Browse for Kubernetes services
	fmt.Println("\n📡 Browsing for Kubernetes services...")
	browseServices("_kube-tunnel._tcp", "local.")

	// Example 3: Register a sample Kubernetes service
	fmt.Println("\n📝 Registering a sample Kubernetes service...")
	registerSampleService()

	// Example 4: Browse again to see our registered service
	fmt.Println("\n📡 Browsing again to see registered service...")
	browseServices("_kube-tunnel._tcp", "local.")

	fmt.Println("\n✅ Zeroconf discovery example completed!")
	fmt.Println("\nWhat this demonstrates:")
	fmt.Println("• Service discovery using zeroconf/mDNS")
	fmt.Println("• Automatic service registration")
	fmt.Println("• Standards-compliant DNS-SD implementation")
	fmt.Println("• Better than custom mDNS for Kubernetes service discovery")
}

func browseServices(serviceType, domain string) {
	entries := make(chan *zeroconf.ServiceEntry, 10)
	done := make(chan bool)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("  ⚠️  Processing panic recovered: %v\n", r)
			}
			done <- true
		}()

		count := 0
		for entry := range entries {
			count++
			fmt.Printf("  📍 Found: %s\n", entry.Instance)
			fmt.Printf("     Service: %s\n", entry.Service)
			fmt.Printf("     Domain: %s\n", entry.Domain)
			fmt.Printf("     Host: %s\n", entry.HostName)
			fmt.Printf("     Port: %d\n", entry.Port)
			fmt.Printf("     IPs: %v\n", formatIPs(entry.AddrIPv4, entry.AddrIPv6))

			if len(entry.Text) > 0 {
				fmt.Printf("     TXT: %v\n", entry.Text)
			}
			fmt.Println()

			// Limit output for demo
			if count >= 3 {
				fmt.Printf("  ... (showing first %d results)\n", count)
				break
			}
		}
		if count == 0 {
			fmt.Printf("  🔍 No %s services found\n", serviceType)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := zeroconf.Browse(ctx, serviceType, domain, entries)
	if err != nil {
		fmt.Printf("  ❌ Browse failed: %v\n", err)
	}

	// Wait for processing to complete or timeout
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		fmt.Printf("  🕐 Browse processing timeout for %s\n", serviceType)
	}
}

func registerSampleService() {
	// Register a sample Kubernetes service
	instanceName := "sample-web-app"
	serviceType := "_kube-tunnel._tcp"
	domain := "local."
	port := 8080

	text := []string{
		"service=web-app",
		"namespace=default",
		"type=kubernetes-service",
		"proxy=kube-tunnel",
		"version=1.0.0",
	}

	server, err := zeroconf.Register(instanceName, serviceType, domain, port, text, nil)
	if err != nil {
		fmt.Printf("  ❌ Registration failed: %v\n", err)
		return
	}

	fmt.Printf("  ✅ Registered: %s.%s%s\n", instanceName, serviceType, domain)
	fmt.Printf("     Port: %d\n", port)
	fmt.Printf("     TXT: %v\n", text)

	// Keep it registered for a short time
	time.Sleep(2 * time.Second)

	server.Shutdown()
	fmt.Printf("  🛑 Unregistered service\n")
}

func formatIPs(ipv4, ipv6 []net.IP) []string {
	var ips []string

	for _, ip := range ipv4 {
		ips = append(ips, ip.String())
	}
	for _, ip := range ipv6 {
		ips = append(ips, ip.String())
	}

	return ips
}
