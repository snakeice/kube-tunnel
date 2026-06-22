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

const podPhaseRunning = "Running"

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
		if pod.Status.Phase == podPhaseRunning {
			svcPort := svc.Spec.Ports[0].TargetPort

			switch svcPort.Type {
			case intstr.Int:
				targetPort := svcPort.IntValue()
				logger.Log.Infof("Using numeric target port %d for service %s/%s (pod: %s)",
					targetPort, namespace, service, pod.Name)
				return pod.Name, targetPort, nil
			case intstr.String:
				port, found := findContainerPortByName(pod, svcPort.String())
				if found {
					logger.Log.Infof("Resolved named port %s to %d for service %s/%s (pod: %s)",
						svcPort.String(), port, namespace, service, pod.Name)
					return pod.Name, port, nil
				}
				logger.Log.Warnf(
					"Could not resolve named port %s, checking application containers",
					svcPort.String(),
				)
			}

			// Try to find the application container port (not Istio sidecar)
			port := findApplicationContainerPort(pod)
			if port > 0 {
				logger.Log.Infof("Found application container port %d for service %s/%s (pod: %s)",
					port, namespace, service, pod.Name)
				return pod.Name, port, nil
			}

			// Fallback to default port
			logger.Log.Warnf("Using fallback port 80 for service %s/%s (pod: %s)",
				namespace, service, pod.Name)
			return pod.Name, 80, nil
		}
	}

	return "", 0, fmt.Errorf("no running pod found for service %s/%s", namespace, service)
}

// findApplicationContainerPort finds the port from the application container (not Istio sidecar).
func findApplicationContainerPort(pod v1.Pod) int {
	// Look for the first non-istio container with ports
	for _, container := range pod.Spec.Containers {
		// Skip Istio sidecar containers
		if container.Name == "istio-proxy" || container.Name == "istio-init" {
			continue
		}

		// Return the first port from the application container
		if len(container.Ports) > 0 {
			return int(container.Ports[0].ContainerPort)
		}
	}

	return 0
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

// GetServicePort returns the target port for a service.
// This is used for service port-forwarding (instead of pod port-forwarding).
func GetServicePort(clientset *kubernetes.Clientset, namespace, service string) (int, error) {
	logger.Log.Infof("Looking up service port: %s/%s", namespace, service)

	svc, err := clientset.CoreV1().
		Services(namespace).
		Get(context.TODO(), service, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get service %s/%s: %w", namespace, service, err)
	}

	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service %s/%s has no ports", namespace, service)
	}

	// Get the target port from the first service port
	svcPort := svc.Spec.Ports[0].TargetPort

	switch svcPort.Type {
	case intstr.Int:
		return svcPort.IntValue(), nil
	case intstr.String:
		// For named ports, we need to resolve the actual port number
		// We'll look at the pods to find the port number
		selector := svc.Spec.Selector
		if len(selector) == 0 {
			return 0, fmt.Errorf("service %s/%s has no selector", namespace, service)
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
			return 0, fmt.Errorf(
				"failed to list pods for service %s/%s: %w",
				namespace,
				service,
				err,
			)
		}

		for _, pod := range pods.Items {
			if pod.Status.Phase == podPhaseRunning {
				port, found := findContainerPortByName(pod, svcPort.String())
				if found {
					return port, nil
				}
			}
		}

		return 0, fmt.Errorf(
			"could not resolve named port %s for service %s/%s",
			svcPort.String(),
			namespace,
			service,
		)
	}

	// Default to port 80 if we can't determine the port
	return 80, nil
}

// GetPodNameForServiceWithPort finds a running pod for a service and resolves the target port
// based on a requested service port.
func GetPodNameForServiceWithPort(
	clientset *kubernetes.Clientset,
	namespace, service string,
	requestedPort int,
) (string, int, error) {
	logger.Log.Infof("Looking up service: %s/%s for port %d", namespace, service, requestedPort)

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

	pods, err := listPodsBySelector(clientset, namespace, selector)
	if err != nil {
		return "", 0, fmt.Errorf(
			"failed to list pods for service %s/%s: %w",
			namespace,
			service,
			err,
		)
	}

	targetPort := resolveServiceTargetPort(svc, requestedPort, namespace, service)

	return findFirstMatchingPod(pods, namespace, service, targetPort, requestedPort)
}

// resolveServiceTargetPort finds the matching service port or falls back to first port.
func resolveServiceTargetPort(
	svc *v1.Service,
	requestedPort int,
	namespace, service string,
) intstr.IntOrString {
	for _, port := range svc.Spec.Ports {
		if int(port.Port) == requestedPort {
			logger.Log.Infof("Found matching service port %d -> target %v for %s/%s",
				requestedPort, port.TargetPort, namespace, service)
			return port.TargetPort
		}
	}

	if len(svc.Spec.Ports) > 0 {
		logger.Log.Warnf(
			"Requested port %d not found, falling back to first service port for %s/%s",
			requestedPort,
			namespace,
			service,
		)
		return svc.Spec.Ports[0].TargetPort
	}

	// This shouldn't happen for a valid service, but handle it gracefully
	return intstr.FromInt(requestedPort)
}

// findFirstMatchingPod iterates through pods to find one that can satisfy the target port.
func findFirstMatchingPod(
	pods *v1.PodList,
	namespace, service string,
	targetPort intstr.IntOrString,
	requestedPort int,
) (string, int, error) {
	for _, pod := range pods.Items {
		if pod.Status.Phase != podPhaseRunning {
			continue
		}

		podName, resolvedPort, found := resolvePodTargetPort(pod, targetPort, namespace, service)
		if found {
			return podName, resolvedPort, nil
		}

		// Try application container port as fallback
		port := findApplicationContainerPort(pod)
		if port > 0 {
			logger.Log.Infof("Found application container port %d for service %s/%s (pod: %s)",
				port, namespace, service, pod.Name)
			return pod.Name, port, nil
		}

		// Final fallback to requested port
		logger.Log.Warnf("Using requested port %d as fallback for service %s/%s (pod: %s)",
			requestedPort, namespace, service, pod.Name)
		return pod.Name, requestedPort, nil
	}

	return "", 0, fmt.Errorf("no running pod found for service %s/%s", namespace, service)
}

// resolvePodTargetPort handles the per-pod target port resolution: numeric, named, or unfound.
func resolvePodTargetPort(
	pod v1.Pod,
	targetPort intstr.IntOrString,
	namespace, service string,
) (string, int, bool) {
	switch targetPort.Type {
	case intstr.Int:
		port := targetPort.IntValue()
		logger.Log.Infof("Using numeric target port %d for service %s/%s (pod: %s)",
			port, namespace, service, pod.Name)
		return pod.Name, port, true
	case intstr.String:
		port, match := findContainerPortByName(pod, targetPort.String())
		if match {
			logger.Log.Infof("Resolved named port %s to %d for service %s/%s (pod: %s)",
				targetPort.String(), port, namespace, service, pod.Name)
			return pod.Name, port, true
		}
		logger.Log.Warnf(
			"Could not resolve named port %s, checking application containers",
			targetPort.String(),
		)
	}
	return "", 0, false
}

// listPodsBySelector lists pods matching the given label selector.
func listPodsBySelector(
	clientset *kubernetes.Clientset,
	namespace string,
	selector map[string]string,
) (*v1.PodList, error) {
	labelParts := make([]string, 0, len(selector))
	for k, v := range selector {
		labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
	}

	return clientset.CoreV1().Pods(namespace).List(
		context.TODO(),
		metav1.ListOptions{LabelSelector: strings.Join(labelParts, ",")},
	)
}
