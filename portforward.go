package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

func startPortForward(ctx context.Context, config *rest.Config, namespace, pod string, localPort, remotePort int) error {
	log.Printf("Setting up port-forward: %s/%s %d:%d", namespace, pod, localPort, remotePort)

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		log.Printf("Failed to create SPDY round tripper: %v", err)
		return err
	}

	hostIP := strings.TrimPrefix(config.Host, "https://")
	url := &url.URL{
		Scheme: "https",
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, pod),
		Host:   hostIP,
	}

	log.Printf("Port-forward URL: %s", url.String())

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}

	log.Printf("Creating port-forward dialer for ports: %v", ports)

	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	fw, err := portforward.New(dialer, ports, ctx.Done(), nil, out, errOut)
	if err != nil {
		log.Printf("Failed to create port-forwarder: %v", err)
		return err
	}

	log.Printf("Starting port-forward for %s/%s", namespace, pod)
	err = fw.ForwardPorts()

	if err != nil {
		log.Printf("Port-forward failed for %s/%s: %v", namespace, pod, err)
		if errOut.Len() > 0 {
			log.Printf("Port-forward stderr: %s", errOut.String())
		}
	} else {
		log.Printf("Port-forward completed for %s/%s", namespace, pod)
	}

	if out.Len() > 0 {
		log.Printf("Port-forward output: %s", out.String())
	}

	return err
}
