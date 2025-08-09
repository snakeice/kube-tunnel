package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snakeice/kube-tunnel/internal/logger"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func GetKubeConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, nil
}

func GetPodNameForService(
	clientset *kubernetes.Clientset,
	namespace, service string,
) (string, int, error) {
	logger.Log.Infof("Looking up service: %s/%s", namespace, service)

	svc, err := clientset.CoreV1().
		Services(namespace).
		Get(context.TODO(), service, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("failed to get service %s/%s: %w", namespace, service, err)
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
		return "", 0, fmt.Errorf(
			"failed to list pods for service %s/%s: %w",
			namespace,
			service,
			err,
		)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase == "Running" {
			svcPort := svc.Spec.Ports[0].TargetPort

			switch svcPort.Type {
			case intstr.Int:
				return pod.Name, svcPort.IntValue(), nil
			case intstr.String:
				port, found := findContainerPortByName(pod, svcPort.String())
				if found {
					return pod.Name, port, nil
				}
			}
			return pod.Name, 80, nil
		}
	}

	return "", 0, fmt.Errorf("no running pod found for service %s/%s", namespace, service)
}

// findContainerPortByName searches for a container port by name in the given pod.
func findContainerPortByName(pod v1.Pod, portName string) (int, bool) {
	for _, container := range pod.Spec.Containers {
		for _, p := range container.Ports {
			if p.Name == portName {
				return int(p.ContainerPort), true
			}
		}
	}
	return 0, false
}
