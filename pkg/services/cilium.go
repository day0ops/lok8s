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

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util/helm"
)

// CiliumManager manages Cilium installation and verification
type CiliumManager struct {
	helmManager   *helm.HelmManager
	binaryManager BinaryManagerInterface
}

// BinaryManagerInterface defines the interface for binary management
type BinaryManagerInterface interface {
	GetBinaryPath() (string, error)
}

// NewCiliumManager creates a new Cilium manager
func NewCiliumManager(helmManager *helm.HelmManager, binaryManager BinaryManagerInterface) *CiliumManager {
	return &CiliumManager{
		helmManager:   helmManager,
		binaryManager: binaryManager,
	}
}

// InstallCilium installs Cilium using Helm
func (cm *CiliumManager) InstallCilium(clusterName string) error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("installing Cilium on cluster %s", clusterName))
	defer func() {
		if status != nil {
			status.End(true)
		}
	}()

	// add cilium repository
	if err := cm.helmManager.AddRepository("cilium", "https://helm.cilium.io/"); err != nil {
		status.End(false)
		return fmt.Errorf("failed to add cilium repository: %w", err)
	}

	// install cilium chart
	values := map[string]interface{}{
		"kubeProxyReplacement": false,
		"envoy": map[string]interface{}{
			"enabled": false,
		},
	}

	if err := cm.helmManager.InstallChart("cilium", "cilium/cilium", "kube-system", values, 5*time.Minute); err != nil {
		status.End(false)
		return fmt.Errorf("failed to install cilium chart: %w", err)
	}

	// wait for cilium pods to be ready before running connectivity test
	if err := cm.WaitForCiliumReady(clusterName); err != nil {
		status.End(false)
		return fmt.Errorf("cilium pods not ready: %w", err)
	}

	return nil
}

// WaitForCiliumReady waits for Cilium to be ready
func (cm *CiliumManager) WaitForCiliumReady(clusterName string) error {
	logger.Debugf("waiting for Cilium to be ready on cluster %s", clusterName)

	client, err := cm.helmManager.GetKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %w", err)
	}

	ctx := context.Background()
	timeout := 10 * time.Minute
	deadline := time.Now().Add(timeout)

	logger.Debugf("waiting for Cilium DaemonSet and operator to be ready...")

	for time.Now().Before(deadline) {
		// check cilium daemonset
		daemonsets, err := client.AppsV1().DaemonSets("kube-system").List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cilium",
		})
		if err != nil {
			logger.Debugf("failed to list cilium daemonsets: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// check cilium operator deployment
		deployments, err := client.AppsV1().Deployments("kube-system").List(ctx, metav1.ListOptions{
			LabelSelector: "io.cilium/app=operator",
		})
		if err != nil {
			logger.Debugf("failed to list cilium operator deployments: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// check cilium pods
		pods, err := client.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cilium",
		})
		if err != nil {
			logger.Debugf("failed to list cilium pods: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// check daemonset readiness
		daemonsetReady := false
		if len(daemonsets.Items) > 0 {
			ds := daemonsets.Items[0]
			if ds.Status.DesiredNumberScheduled > 0 && ds.Status.NumberReady == ds.Status.DesiredNumberScheduled {
				daemonsetReady = true
			}
		}

		// check operator readiness
		operatorReady := false
		if len(deployments.Items) > 0 {
			deployment := deployments.Items[0]
			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
				operatorReady = true
			}
		}

		// check pod readiness
		readyPods := 0
		for _, pod := range pods.Items {
			if pod.Status.Phase == "Running" && pod.Status.ContainerStatuses != nil {
				allContainersReady := true
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if !containerStatus.Ready {
						allContainersReady = false
						break
					}
				}
				if allContainersReady {
					readyPods++
				}
			}
		}

		logger.Debugf("Cilium status - DaemonSet: %v, Operator: %v, Pods: %d/%d",
			daemonsetReady, operatorReady, readyPods, len(pods.Items))

		// all components ready
		if daemonsetReady && operatorReady && readyPods == len(pods.Items) && len(pods.Items) > 0 {
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Cilium to be ready on cluster %s", clusterName)
}

// GenerateCiliumManifest generates a Cilium manifest file from the helm chart
// returns the path to the generated manifest file
func (cm *CiliumManager) GenerateCiliumManifest(clusterName string) (string, error) {
	logger.Debugf("generating Cilium manifest for cluster %s", clusterName)

	// cilium values matching the InstallCilium function
	values := map[string]interface{}{
		"kubeProxyReplacement": false,
		"envoy": map[string]interface{}{
			"enabled": false,
		},
	}

	// render the helm chart to manifests
	manifestYAML, err := cm.helmManager.TemplateChart("cilium", "cilium/cilium", "kube-system", values)
	if err != nil {
		return "", fmt.Errorf("failed to template Cilium chart: %w", err)
	}

	// create temporary file for the manifest
	tmpDir := os.TempDir()
	manifestPath := filepath.Join(tmpDir, fmt.Sprintf("cilium-%s-manifest.yaml", clusterName))

	// write manifest to file
	if err := os.WriteFile(manifestPath, manifestYAML, 0644); err != nil {
		return "", fmt.Errorf("failed to write Cilium manifest to file: %w", err)
	}

	logger.Debugf("generated Cilium manifest file: %s", manifestPath)
	return manifestPath, nil
}
