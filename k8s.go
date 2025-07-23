package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func getKubeConfig() (*rest.Config, error) {
	log.Printf("Attempting to get Kubernetes configuration")

	if config, err := rest.InClusterConfig(); err == nil {
		log.Printf("Using in-cluster Kubernetes configuration")
		return config, nil
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	log.Printf("Using kubeconfig file: %s", kubeconfig)

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Printf("Failed to build config from kubeconfig: %v", err)
		return nil, err
	}

	log.Printf("Successfully loaded Kubernetes configuration (host: %s)", config.Host)
	return config, nil
}

func getPodNameForService(clientset *kubernetes.Clientset, namespace, service string) (string, error) {
	log.Printf("Looking up service: %s/%s", namespace, service)

	svc, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), service, metav1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get service %s/%s: %v", namespace, service, err)
		return "", err
	}

	log.Printf("Found service %s/%s", namespace, service)

	selector := svc.Spec.Selector
	if len(selector) == 0 {
		log.Printf("Service %s/%s has no selector", namespace, service)
		return "", fmt.Errorf("service %s/%s has no selector", namespace, service)
	}

	log.Printf("Service selector for %s/%s: %v", namespace, service, selector)

	labelSelector := []string{}
	for k, v := range selector {
		labelSelector = append(labelSelector, fmt.Sprintf("%s=%s", k, v))
	}

	selectorString := strings.Join(labelSelector, ",")
	log.Printf("Searching for pods with selector: %s", selectorString)

	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selectorString,
	})
	if err != nil {
		log.Printf("Failed to list pods for service %s/%s: %v", namespace, service, err)
		return "", err
	}

	log.Printf("Found %d pods for service %s/%s", len(pods.Items), namespace, service)

	for _, pod := range pods.Items {
		log.Printf("Pod %s status: %s", pod.Name, pod.Status.Phase)
		if pod.Status.Phase == "Running" {
			log.Printf("Selected running pod: %s", pod.Name)
			return pod.Name, nil
		}
	}

	log.Printf("No running pods found for service %s/%s", namespace, service)
	return "", fmt.Errorf("no running pod for %s/%s", namespace, service)
}
