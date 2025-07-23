package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
)

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Incoming request: %s %s from %s (Host: %s)", r.Method, r.URL.Path, r.RemoteAddr, r.Host)

	host := r.Host
	service, namespace, err := parseHost(host)
	if err != nil {
		log.Printf("Failed to parse host '%s': %v", host, err)
		http.Error(w, "Invalid host", http.StatusBadRequest)
		return
	}

	log.Printf("Parsed host: service=%s, namespace=%s", service, namespace)

	localPort, err := ensurePortForward(service, namespace, 8080)
	if err != nil {
		log.Printf("Failed to ensure port-forward for %s.%s: %v", service, namespace, err)
		http.Error(w, "Error creating port-forward", http.StatusInternalServerError)
		return
	}

	log.Printf("Port-forward ready: %s.%s -> localhost:%d", service, namespace, localPort)

	proxy := &httputil.ReverseProxy{Director: func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("localhost:%d", localPort)
		log.Printf("Proxying %s %s to %s", req.Method, req.URL.Path, req.URL.Host)
	}}
	proxy.ServeHTTP(w, r)

	log.Printf("Request completed: %s %s", r.Method, r.URL.Path)
}
