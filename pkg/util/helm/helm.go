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

package helm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/repo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// HelmManager manages Helm operations
type HelmManager struct {
	kubeconfigPath string
	settings       *cli.EnvSettings
}

// NewHelmManager creates a new Helm manager
func NewHelmManager(kubeconfigPath string) *HelmManager {
	settings := cli.New()
	// set kubeconfig path via environment variable
	os.Setenv("KUBECONFIG", kubeconfigPath)

	return &HelmManager{
		kubeconfigPath: kubeconfigPath,
		settings:       settings,
	}
}

// AddRepository adds a Helm repository
func (hm *HelmManager) AddRepository(name, url string) error {
	logger.Debugf("adding Helm repository: %s -> %s", name, url)

	// check if repository already exists
	repos, err := hm.ListRepositories()
	if err != nil {
		return fmt.Errorf("failed to list repositories: %w", err)
	}

	for _, repo := range repos {
		if repo.Name == name {
			logger.Debugf("repository %s already exists", name)
			return nil
		}
	}

	// add repository using helm CLI
	cmd := exec.Command("helm", "repo", "add", name, url)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add repository %s: %w", name, err)
	}

	// update repository
	cmd = exec.Command("helm", "repo", "update", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update repository %s: %w", name, err)
	}

	logger.Debugf("added Helm repository: %s", name)
	return nil
}

// ListRepositories lists all Helm repositories
func (hm *HelmManager) ListRepositories() ([]*repo.Entry, error) {
	repoFile := hm.settings.RepositoryConfig
	rf, err := repo.LoadFile(repoFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load repository file: %w", err)
	}

	var repos []*repo.Entry
	repos = append(repos, rf.Repositories...)

	return repos, nil
}

// InstallChart installs a Helm chart
func (hm *HelmManager) InstallChart(releaseName, chartName, namespace string, values map[string]interface{}, timeout time.Duration) error {
	logger.Debugf("installing Helm chart: %s/%s in namespace %s", chartName, releaseName, namespace)

	// Check if release already exists
	exists, err := hm.ReleaseExists(releaseName, namespace)
	if err != nil {
		return fmt.Errorf("failed to check if release exists: %w", err)
	}

	if exists {
		logger.Debugf("release %s already exists, upgrading instead", releaseName)
		return hm.UpgradeChart(releaseName, chartName, namespace, values, timeout)
	}

	// Create action configuration
	actionConfig, err := hm.getActionConfig(namespace)
	if err != nil {
		return fmt.Errorf("failed to get action config: %w", err)
	}

	// Create install action
	install := action.NewInstall(actionConfig)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.CreateNamespace = true
	install.Timeout = timeout
	install.Wait = true

	// Get chart
	chartPath, err := install.ChartPathOptions.LocateChart(chartName, hm.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	// Install chart
	// Temporarily suppress stderr to avoid kubectl warnings interfering with spinner
	originalStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		os.Stderr = devNull
		defer func() {
			os.Stderr = originalStderr
			devNull.Close()
		}()
	}

	release, err := install.RunWithContext(context.Background(), chart, values)
	if err != nil {
		// Restore stderr before returning error so it can be displayed
		if devNull != nil {
			os.Stderr = originalStderr
		}
		return fmt.Errorf("failed to install chart: %w", err)
	}

	logger.Debugf("installed Helm chart: %s/%s (version: %s)", chartName, releaseName, release.Chart.Metadata.Version)
	return nil
}

// UpgradeChart upgrades a Helm chart
func (hm *HelmManager) UpgradeChart(releaseName, chartName, namespace string, values map[string]interface{}, timeout time.Duration) error {
	logger.Debugf("upgrading Helm chart: %s/%s in namespace %s", chartName, releaseName, namespace)

	// Create action configuration
	actionConfig, err := hm.getActionConfig(namespace)
	if err != nil {
		return fmt.Errorf("failed to get action config: %w", err)
	}

	// Create upgrade action
	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = namespace
	upgrade.Timeout = timeout
	upgrade.Wait = true

	// Get chart
	chartPath, err := upgrade.ChartPathOptions.LocateChart(chartName, hm.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	// Upgrade chart
	// Temporarily suppress stderr to avoid kubectl warnings interfering with spinner
	originalStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		os.Stderr = devNull
		defer func() {
			os.Stderr = originalStderr
			devNull.Close()
		}()
	}

	release, err := upgrade.RunWithContext(context.Background(), releaseName, chart, values)
	if err != nil {
		// Restore stderr before returning error so it can be displayed
		if devNull != nil {
			os.Stderr = originalStderr
		}
		return fmt.Errorf("failed to upgrade chart: %w", err)
	}

	logger.Debugf("upgraded Helm chart: %s/%s (version: %s)", chartName, releaseName, release.Chart.Metadata.Version)
	return nil
}

// UninstallChart uninstalls a Helm chart
func (hm *HelmManager) UninstallChart(releaseName, namespace string) error {
	logger.Debugf("uninstalling Helm chart: %s in namespace %s", releaseName, namespace)

	// Create action configuration
	actionConfig, err := hm.getActionConfig(namespace)
	if err != nil {
		return fmt.Errorf("failed to get action config: %w", err)
	}

	// Create uninstall action
	uninstall := action.NewUninstall(actionConfig)

	// Uninstall chart
	_, err = uninstall.Run(releaseName)
	if err != nil {
		return fmt.Errorf("failed to uninstall chart: %w", err)
	}

	logger.Infof("uninstalled Helm chart: %s", releaseName)
	return nil
}

// ReleaseExists checks if a Helm release exists
func (hm *HelmManager) ReleaseExists(releaseName, namespace string) (bool, error) {
	// Create action configuration
	actionConfig, err := hm.getActionConfig(namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get action config: %w", err)
	}

	// Create list action
	list := action.NewList(actionConfig)
	list.AllNamespaces = false

	// List releases
	releases, err := list.Run()
	if err != nil {
		return false, fmt.Errorf("failed to list releases: %w", err)
	}

	// Check if release exists
	for _, release := range releases {
		if release.Name == releaseName && release.Namespace == namespace {
			return true, nil
		}
	}

	return false, nil
}

// WaitForReleaseReady waits for a Helm release to be ready
func (hm *HelmManager) WaitForReleaseReady(releaseName, namespace string, timeout time.Duration) error {
	logger.Debugf("waiting for Helm release %s to be ready in namespace %s", releaseName, namespace)

	// Get Kubernetes client
	client, err := hm.GetKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// Wait for release to be ready using retry mechanism
	return util.LocalRetry(func() error {
		// Check if release exists and is deployed
		exists, err := hm.ReleaseExists(releaseName, namespace)
		if err != nil {
			return fmt.Errorf("failed to check release existence: %w", err)
		}

		if !exists {
			return fmt.Errorf("release %s does not exist", releaseName)
		}

		// Get release status
		actionConfig, err := hm.getActionConfig(namespace)
		if err != nil {
			return fmt.Errorf("failed to get action config: %w", err)
		}

		status := action.NewStatus(actionConfig)
		release, err := status.Run(releaseName)
		if err != nil {
			return fmt.Errorf("failed to get release status: %w", err)
		}

		if release.Info.Status != "deployed" {
			return fmt.Errorf("release %s is not deployed yet, status: %s", releaseName, release.Info.Status)
		}

		// Check if all pods are ready
		pods, err := client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		for _, pod := range pods.Items {
			// Check if pod belongs to this release
			if pod.Labels["app.kubernetes.io/instance"] == releaseName {
				if pod.Status.Phase != "Running" {
					return fmt.Errorf("pod %s is not running yet, phase: %s", pod.Name, pod.Status.Phase)
				}

				// Check if all containers are ready
				for _, container := range pod.Status.ContainerStatuses {
					if !container.Ready {
						return fmt.Errorf("container %s in pod %s is not ready", container.Name, pod.Name)
					}
				}
			}
		}

		return nil
	}, timeout)
}

// getActionConfig creates a Helm action configuration
func (hm *HelmManager) getActionConfig(namespace string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)

	// Initialize with settings
	if err := actionConfig.Init(hm.settings.RESTClientGetter(), namespace, "secret", func(format string, v ...interface{}) {
		logger.Debugf(format, v...)
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize action config: %w", err)
	}

	return actionConfig, nil
}

// GetKubernetesClient creates a Kubernetes client
func (hm *HelmManager) GetKubernetesClient() (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", hm.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return client, nil
}

// ListReleases lists all Helm releases
func (hm *HelmManager) ListReleases(namespace string) ([]*release.Release, error) {
	// Create action configuration
	actionConfig, err := hm.getActionConfig(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get action config: %w", err)
	}

	// Create list action
	list := action.NewList(actionConfig)
	list.AllNamespaces = false

	// List releases
	releases, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	return releases, nil
}

// TemplateChart renders a Helm chart to Kubernetes manifests using the Helm library
func (hm *HelmManager) TemplateChart(releaseName, chartName, namespace string, values map[string]interface{}) ([]byte, error) {
	logger.Debugf("rendering Helm chart: %s/%s to manifests", chartName, releaseName)

	// ensure repository is added and updated
	// extract repo name from chart (e.g., "cilium/cilium" -> "cilium")
	chartParts := strings.Split(chartName, "/")
	if len(chartParts) != 2 {
		return nil, fmt.Errorf("invalid chart name format, expected repo/chart: %s", chartName)
	}
	repoName := chartParts[0]

	// add cilium repository if needed
	if repoName == "cilium" {
		if err := hm.AddRepository("cilium", "https://helm.cilium.io/"); err != nil {
			return nil, fmt.Errorf("failed to add cilium repository: %w", err)
		}
		// update repository to ensure we have the latest chart
		cmd := exec.Command("helm", "repo", "update", "cilium")
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to update cilium repository: %w", err)
		}
	}

	// create a minimal action config for templating (doesn't require valid kubeconfig)
	// use a dummy namespace since we're not actually connecting to a cluster
	actionConfig := new(action.Configuration)
	dummyNamespace := "default"
	if err := actionConfig.Init(hm.settings.RESTClientGetter(), dummyNamespace, "memory", func(format string, v ...interface{}) {
		logger.Debugf(format, v...)
	}); err != nil {
		// if initialization fails (e.g., no kubeconfig), we can still proceed with templating
		// by using engine directly
		logger.Debugf("action config initialization failed (this is OK for templating): %v", err)
	}

	// create install action for templating
	install := action.NewInstall(actionConfig)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.DryRun = true
	install.Replace = true
	install.ClientOnly = true

	// dummy versioning so override the conditions in the charts
	install.KubeVersion = &chartutil.KubeVersion{
		Version: "v1.30.0",
		Major:   "1",
		Minor:   "30",
	}

	// locate and load the chart
	chartPath, err := install.ChartPathOptions.LocateChart(chartName, hm.settings)
	if err != nil {
		return nil, fmt.Errorf("failed to locate chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart: %w", err)
	}

	// use install.Run to generate the manifest (this handles ordering automatically)
	release, err := install.RunWithContext(context.Background(), chart, values)
	if err != nil {
		return nil, fmt.Errorf("failed to template chart: %w", err)
	}

	// return the manifest from the release
	output := []byte(release.Manifest)
	logger.Debugf("rendered Helm chart %s to manifests (%d bytes)", chartName, len(output))
	return output, nil
}
