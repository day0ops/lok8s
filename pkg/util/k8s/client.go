// MIT License
//
// Copyright (c) 2025 lok8s
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/day0ops/lok8s/pkg/logger"
)

// ClientManager manages Kubernetes client operations
type ClientManager struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	config        *rest.Config
	contextName   string
}

// NewClientManagerForContext creates a new Kubernetes client manager for a specific context
func NewClientManagerForContext(contextName string) (*ClientManager, error) {
	config, err := getKubeConfigForContext(contextName)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config for context %s: %w", contextName, err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &ClientManager{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		config:        config,
		contextName:   contextName,
	}, nil
}

// GetClientset returns the Kubernetes clientset
func (cm *ClientManager) GetClientset() *kubernetes.Clientset {
	return cm.clientset
}

// GetDynamicClient returns the dynamic client
func (cm *ClientManager) GetDynamicClient() dynamic.Interface {
	return cm.dynamicClient
}

// GetConfig returns the Kubernetes config
func (cm *ClientManager) GetConfig() *rest.Config {
	return cm.config
}

// WaitForNodesReady waits for all nodes in the cluster to be ready
func (cm *ClientManager) WaitForNodesReady(timeout time.Duration) error {
	logger.Debug("waiting for nodes to be ready...")
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		nodes, err := cm.clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			logger.Debugf("failed to list nodes: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		allReady := true
		for _, node := range nodes.Items {
			isReady := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					isReady = true
					break
				}
			}
			if !isReady {
				allReady = false
				break
			}
		}

		if allReady {
			logger.Debug("all nodes are ready")
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timeout waiting for nodes to be ready")
}

// WaitForNodesReadyWithCount waits for a specific number of nodes to be ready
func (cm *ClientManager) WaitForNodesReadyWithCount(expectedNodes int, timeout time.Duration) error {
	logger.Debugf("waiting for %d nodes to be ready...", expectedNodes)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		nodes, err := cm.clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			logger.Debugf("failed to list nodes: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		readyNodes := 0
		for _, node := range nodes.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					readyNodes++
					break
				}
			}
		}

		if readyNodes == expectedNodes {
			logger.Debugf("all %d nodes are ready", expectedNodes)
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("expected %d ready nodes, timeout after %v", expectedNodes, timeout)
}

// ApplyManifest applies a Kubernetes manifest using the dynamic client
func (cm *ClientManager) ApplyManifest(manifest string) error {
	logger.Debugf("applying Kubernetes manifest using client manager")

	// parse the YAML manifest
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("failed to decode manifest: %w", err)
		}

		// convert to unstructured object
		obj := &unstructured.Unstructured{}
		if err := runtime.DecodeInto(unstructured.UnstructuredJSONScheme, rawObj.Raw, obj); err != nil {
			return fmt.Errorf("failed to decode object: %w", err)
		}

		// get the resource
		gvr := schema.GroupVersionResource{
			Group:    obj.GroupVersionKind().Group,
			Version:  obj.GroupVersionKind().Version,
			Resource: getResourceFromKind(obj.GetKind()),
		}

		// apply the resource
		if err := cm.applyResource(gvr, obj); err != nil {
			return fmt.Errorf("failed to apply resource %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		logger.Debugf("applied resource: %s/%s", obj.GetKind(), obj.GetName())
	}

	logger.Debugf("manifest applied successfully")
	return nil
}

// CheckNamespaceExists checks if a namespace exists
func (cm *ClientManager) CheckNamespaceExists(namespace string) error {
	_, err := cm.clientset.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace %s not found: %w", namespace, err)
	}
	return nil
}

// CheckDeploymentReady checks if a deployment is ready
func (cm *ClientManager) CheckDeploymentReady(namespace, name string) error {
	deployment, err := cm.clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	if deployment.Status.ReadyReplicas == 0 || deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
		return fmt.Errorf("deployment %s/%s not ready: %d/%d replicas ready",
			namespace, name, deployment.Status.ReadyReplicas, *deployment.Spec.Replicas)
	}

	return nil
}

// CheckDaemonSetReady checks if a daemonset is ready
func (cm *ClientManager) CheckDaemonSetReady(namespace, name string) error {
	daemonset, err := cm.clientset.AppsV1().DaemonSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get daemonset %s/%s: %w", namespace, name, err)
	}

	if daemonset.Status.DesiredNumberScheduled == 0 ||
		daemonset.Status.NumberReady != daemonset.Status.DesiredNumberScheduled {
		return fmt.Errorf("daemonset %s/%s not ready: %d/%d pods ready",
			namespace, name, daemonset.Status.NumberReady, daemonset.Status.DesiredNumberScheduled)
	}

	return nil
}

// applyResource applies a single resource using the dynamic client
func (cm *ClientManager) applyResource(gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	ctx := context.Background()

	// try to get the resource first
	existing, err := cm.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		// resource doesn't exist, create it
		_, err = cm.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Create(ctx, obj, metav1.CreateOptions{})
		return err
	}

	// resource exists, update it
	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = cm.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

// getResourceFromKind maps Kubernetes resource kinds to their resource names
func getResourceFromKind(kind string) string {
	kindToResource := map[string]string{
		"IPAddressPool":   "ipaddresspools",
		"L2Advertisement": "l2advertisements",
		"ConfigMap":       "configmaps",
		"Service":         "services",
		"Deployment":      "deployments",
		"DaemonSet":       "daemonsets",
		"Namespace":       "namespaces",
		"Pod":             "pods",
		"Node":            "nodes",
	}

	if resource, exists := kindToResource[kind]; exists {
		return resource
	}

	// fallback: convert kind to lowercase and pluralize
	return strings.ToLower(kind) + "s"
}

// getKubeConfigForContext creates a kubernetes config for a specific context
func getKubeConfigForContext(contextName string) (*rest.Config, error) {
	// get kubeconfig path
	kubeconfig, err := GetKubeConfigPath()
	if err != nil {
		return nil, err
	}

	// create config with specific context
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()

	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config for context %s: %w", contextName, err)
	}

	return config, nil
}
