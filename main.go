package main

import (
	"log"
	"net/http"
)

func main() {
	log.Println("Starting kube-tunnel proxy server...")

	http.HandleFunc("/", proxyHandler)

	log.Println("Proxy started on :80")
	log.Println("Ready to handle requests in format: http://service.namespace.svc.cluster.local")

	err := http.ListenAndServe(":80", nil)
	log.Fatalf("Server failed to start: %v", err)
}
