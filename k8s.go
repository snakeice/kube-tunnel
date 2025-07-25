package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func getKubeConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %v", err)
	}

	return config, nil
}

func getPodNameForService(clientset *kubernetes.Clientset, namespace, service string) (string, int32, error) {
	log.Printf("Looking up service: %s/%s", namespace, service)

	svc, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), service, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("failed to get service %s/%s: %v", namespace, service, err)
	}

	selector := svc.Spec.Selector
	if len(selector) == 0 {
		return "", 0, fmt.Errorf("service %s/%s has no selector", namespace, service)
	}

	labelSelector := []string{}
	for k, v := range selector {
		labelSelector = append(labelSelector, fmt.Sprintf("%s=%s", k, v))
	}

	selectorString := strings.Join(labelSelector, ",")
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selectorString,
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to list pods for service %s/%s: %v", namespace, service, err)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase == "Running" {
			svcPort := svc.Spec.Ports[0].TargetPort

			switch svcPort.Type {
			case intstr.Int:
				return pod.Name, int32(svcPort.IntValue()), nil
			default:
				for _, container := range pod.Spec.Containers {
					for _, p := range container.Ports {
						if p.Name == svcPort.String() {
							return pod.Name, p.ContainerPort, nil
						}
					}
				}
			}
			return pod.Name, 80, nil
		}
	}

	return "", 0, fmt.Errorf("no running pod found for service %s/%s", namespace, service)
}
