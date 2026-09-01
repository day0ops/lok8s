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

package native

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/services"
	"github.com/day0ops/lok8s/pkg/util/helm"
	"github.com/day0ops/lok8s/pkg/util/k8s"
	"github.com/day0ops/lok8s/pkg/util/version"
)

// Manager manages native (Apple Virtualization / `container` CLI) clusters
type Manager struct {
	client         *ContainerClient
	helmManager    *helm.HelmManager
	ciliumManager  *services.CiliumManager
	metallbManager *services.MetalLBManager
}

// CreateOptions contains options for creating native clusters
type CreateOptions struct {
	Project        string
	SubnetCIDR     string
	NumClusters    int
	NodeCount      int
	K8sVersion     string
	InstallMetalLB bool
	CNI            string
	Recreate       bool
}

// DeleteOptions contains options for deleting native clusters
type DeleteOptions struct {
	Project     string
	NumClusters int
	Force       bool
}

// StatusOptions contains options for checking native cluster status
type StatusOptions struct {
	Project     string
	NumClusters int
}

// LoadImageOptions contains options for loading images into native clusters
type LoadImageOptions struct {
	Project     string
	Image       string
	NumClusters int
}

// projectNetworkName returns the `container network` name for a project.
// `container network create` rejects names with uppercase letters, unlike
// project names themselves which are free-form, so this normalizes to
// lowercase.
func projectNetworkName(project string) string {
	return config.NativeNetworkNamePrefix + strings.ToLower(project)
}

// clusterAndContextName returns the native cluster name (used as the prefix
// for node container names) and kubeconfig context name for the i-th cluster
// of a project. Both are project-scoped so that two different projects never
// collide on the same underlying node container names.
func clusterAndContextName(project string, numClusters, i int) (clusterName, contextName string) {
	if numClusters == 1 {
		// if only one cluster, don't add suffix
		return project, project
	}
	name := fmt.Sprintf("%s-%d", project, i)
	return name, name
}

// nodeNamesForCluster returns the existing node container names that belong
// to clusterName specifically: clusterName+"-control-plane", and any
// clusterName+"-worker-N". A bare prefix match isn't precise enough now that
// cluster names are project-scoped - it would also match an unrelated
// cluster whose name happens to start with clusterName followed by a hyphen
// (e.g. "myproject" matching "myproject-2-control-plane" from a different,
// unrelated project literally named "myproject-2").
func (m *Manager) nodeNamesForCluster(clusterName string) ([]string, error) {
	candidates, err := m.client.ListNodeNames(clusterName + "-")
	if err != nil {
		return nil, err
	}

	cpName := clusterName + "-control-plane"
	workerPrefix := clusterName + "-worker-"

	var matched []string
	for _, name := range candidates {
		if name == cpName {
			matched = append(matched, name)
			continue
		}
		if suffix, ok := strings.CutPrefix(name, workerPrefix); ok {
			if _, err := strconv.Atoi(suffix); err == nil {
				matched = append(matched, name)
			}
		}
	}

	return matched, nil
}

// NewManager creates a new native manager
func NewManager() *Manager {
	k8sConfigPath, _ := k8s.GetKubeConfigPath()
	helmManager := helm.NewHelmManager(k8sConfigPath)

	return &Manager{
		client:        NewContainerClient(),
		helmManager:   helmManager,
		ciliumManager: services.NewCiliumManager(helmManager, nil), // native doesn't need a binary manager
		// native nodes are directly IP-addressable on the project's routable
		// network (like minikube's VMs), unlike kind's NAT'd Docker network,
		// so it uses the same bounded octet range minikube does
		metallbManager: services.NewMetalLBManagerWithOptions(helmManager, config.MetalLBRangeMinLastOctet, config.MetalLBRangeMaxLastOctet),
	}
}

// CreateClusters creates multiple native clusters
func (m *Manager) CreateClusters(opts *CreateOptions) error {
	logger.Infof("-----> 📢 creating %d native cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	if err := m.checkPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	if opts.CNI != "cilium" {
		return fmt.Errorf("the native provider only supports the cilium CNI, got: %s", opts.CNI)
	}

	image, err := k3sImage(opts.K8sVersion)
	if err != nil {
		return fmt.Errorf("failed to resolve Kubernetes version: %w", err)
	}

	// one network per project (not per cluster), so every cluster and node
	// within a project shares one routable, isolated segment
	networkName, err := m.ensureNetwork(opts)
	if err != nil {
		return fmt.Errorf("failed to set up network: %w", err)
	}

	for i := 1; i <= opts.NumClusters; i++ {
		clusterName, contextName := clusterAndContextName(opts.Project, opts.NumClusters, i)

		cpIP, err := m.createCluster(clusterName, contextName, image, networkName, opts)
		if err != nil {
			return fmt.Errorf("failed to create cluster %s: %w", clusterName, err)
		}

		// Cilium must install before MetalLB: unlike kind/minikube's shared-kernel
		// containers, native nodes have no pod networking at all until Cilium is
		// up, and MetalLB's controller/speaker pods (unlike Cilium's own
		// daemonset, which runs on the host network) need it to reach Ready -
		// installing MetalLB first would otherwise hang until its own helm
		// install timeout.
		//
		// kubeProxyReplacement is also required: cross-node pod traffic to a
		// ClusterIP backed by a host-networked pod (like k3s's own apiserver
		// Service) hangs on TLS handshakes under kube-proxy's iptables DNAT
		// in this environment. k3s's own kube-proxy is left running (see
		// controlPlaneArgs) since Cilium's replacement intercepts ahead of
		// it once up, without leaving a gap before Cilium itself is ready.
		ciliumValues := map[string]interface{}{
			"kubeProxyReplacement": true,
			"k8sServiceHost":       cpIP,
			"k8sServicePort":       k3sAPIPort,
		}
		if err := m.ciliumManager.InstallCiliumWithValues(contextName, ciliumValues); err != nil {
			logger.Errorf("failed to install Cilium on %s: %v", contextName, err)
		}

		if opts.InstallMetalLB {
			// initialize tracking before first cluster configuration
			if i == 1 {
				if err := m.metallbManager.InitializeTracking(opts.Project); err != nil {
					logger.Warnf("failed to initialize MetalLB tracking: %v", err)
				}
			}

			// pin the controller to the control-plane node: the apiserver's
			// admission webhook calls to it hang when it lands on a worker
			// node, the same cross-node-to-remote-pod-IP hang diagnosed for
			// the apiserver's own Service traffic above, but this path
			// (a direct apiserver-to-endpoint-IP dial for webhook calls)
			// isn't covered by Cilium's kube-proxy replacement since it
			// bypasses Service resolution entirely.
			metallbValues := map[string]interface{}{
				"controller": map[string]interface{}{
					"resources": map[string]interface{}{
						"requests": map[string]interface{}{
							"cpu":    "100m",
							"memory": "100Mi",
						},
					},
					"nodeSelector": map[string]interface{}{
						"node-role.kubernetes.io/control-plane": "true",
					},
				},
			}
			if err := m.metallbManager.InstallMetalLBWithValues(contextName, metallbValues); err != nil {
				logger.Errorf("failed to install MetalLB on %s: %v", contextName, err)
			} else {
				if err := m.metallbManager.ConfigureMetalLB(contextName, cpIP, i, opts.NumClusters, opts.Project); err != nil {
					logger.Errorf("failed to configure MetalLB on %s: %v", contextName, err)
				}
			}
		}
	}

	logger.Infof("🎉 successfully created %d native cluster(s)", opts.NumClusters)

	if err := m.printClusterStatusTable(&StatusOptions{Project: opts.Project, NumClusters: opts.NumClusters}); err != nil {
		logger.Warnf("failed to show cluster status: %v", err)
	}

	return nil
}

// DeleteClusters deletes multiple native clusters
func (m *Manager) DeleteClusters(opts *DeleteOptions) error {
	logger.Infof("-----> 🚨 deleting %d native cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	for i := 1; i <= opts.NumClusters; i++ {
		clusterName, contextName := clusterAndContextName(opts.Project, opts.NumClusters, i)

		status := logger.NewStatus()
		status.Start(fmt.Sprintf("deleting native cluster %s", clusterName))
		success := true

		nodeNames, err := m.nodeNamesForCluster(clusterName)
		if err != nil {
			success = false
			logger.Errorf("failed to list nodes for cluster %s: %v", clusterName, err)
		} else if len(nodeNames) > 0 {
			if err := m.client.Delete(nodeNames...); err != nil {
				success = false
				logger.Errorf("failed to delete nodes for cluster %s: %v", clusterName, err)
			}
		}

		if err := k8s.DeleteContext(contextName); err != nil {
			success = false
			logger.Errorf("failed to delete context %s: %v", contextName, err)
		}

		status.End(success)
	}

	// the project network is shared by every cluster in the project, so only
	// tear it down when the caller explicitly asks for a full cleanup
	if opts.Force {
		networkName := projectNetworkName(opts.Project)
		if err := m.client.DeleteNetwork(networkName); err != nil {
			logger.Warnf("failed to delete network %s: %v", networkName, err)
		} else {
			logger.Infof("deleted network %s", networkName)
		}
	}

	configManager := config.NewConfigManager()
	if err := configManager.DeleteConfig(opts.Project); err != nil {
		logger.Warnf("failed to delete project config for %s: %v", opts.Project, err)
	} else {
		logger.Infof("deleted project configuration: %s", opts.Project)
	}

	logger.Infof("successfully deleted %d native cluster(s)", opts.NumClusters)
	return nil
}

// StatusClusters shows the status of native clusters
func (m *Manager) StatusClusters(opts *StatusOptions) error {
	logger.Infof("-----> 📊 checking status of %d native cluster(s) for project %s <-----", opts.NumClusters, opts.Project)
	return m.printClusterStatusTable(opts)
}

// LoadImage loads an image into native clusters. Not yet supported: `container
// image load` only seeds the local image store, not a running node's
// containerd, so there is no direct equivalent to kind's `kind load
// docker-image` today.
func (m *Manager) LoadImage(_ *LoadImageOptions) error {
	return fmt.Errorf("image loading is not yet supported for the native provider")
}

// ListClusterNames returns the names of all existing native clusters, derived
// from control-plane node names (each cluster has exactly one)
func (m *Manager) ListClusterNames() ([]string, error) {
	names, err := m.client.ListNodeNames("")
	if err != nil {
		return nil, fmt.Errorf("failed to list native nodes: %w", err)
	}

	var clusters []string
	for _, name := range names {
		if clusterName, ok := strings.CutSuffix(name, "-control-plane"); ok {
			clusters = append(clusters, clusterName)
		}
	}

	return clusters, nil
}

// checkPrerequisites checks if the native provider's requirements are met
func (m *Manager) checkPrerequisites() error {
	// this is Apple's own hard requirement for the `container` CLI, not a
	// choice lok8s is making
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("the native provider requires macOS on Apple Silicon (darwin/arm64), detected %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	installedVersion, err := m.client.Version()
	if err != nil {
		return fmt.Errorf("container CLI not found or not working: %w (install it from github.com/apple/container)", err)
	}

	if installedVersion == "1.2.0" {
		return fmt.Errorf("container CLI version 1.2.0 has known issues, please upgrade to %s or later", config.NativeContainerMinSupportedVersion)
	}
	if version.Compare(config.NativeContainerMinSupportedVersion, installedVersion) > 0 {
		return fmt.Errorf("container CLI version %s is too old. Minimum supported version is %s", installedVersion, config.NativeContainerMinSupportedVersion)
	}
	logger.Debugf("using container CLI version: %s", installedVersion)

	if !m.client.SystemRunning() {
		return fmt.Errorf("container system services are not running, start them with: container system start")
	}

	return nil
}

// ensureNetwork creates the project's shared network if it doesn't already
// exist, picking a free subnet if the requested one is already in use by
// another project's network. opts.SubnetCIDR is updated to whichever subnet
// actually ends up in use, so the caller can persist it.
func (m *Manager) ensureNetwork(opts *CreateOptions) (string, error) {
	networkName := projectNetworkName(opts.Project)

	exists, err := m.client.NetworkExists(networkName)
	if err != nil {
		return "", fmt.Errorf("failed to check for existing network %s: %w", networkName, err)
	}
	if exists {
		return networkName, nil
	}

	freeSubnet, err := FindFreeSubnet(m.client, opts.SubnetCIDR, 1, 50)
	if err != nil {
		return "", fmt.Errorf("failed to find a free subnet: %w", err)
	}
	if freeSubnet != opts.SubnetCIDR {
		logger.Debugf("using generated subnet %s (requested subnet %s is already in use)", freeSubnet, opts.SubnetCIDR)
		opts.SubnetCIDR = freeSubnet
	}

	if err := m.client.CreateNetwork(networkName, freeSubnet); err != nil {
		return "", fmt.Errorf("failed to create network %s: %w", networkName, err)
	}

	return networkName, nil
}

// createCluster creates a single native cluster's control-plane node and
// workers, waits for them to become ready, and merges the control-plane's
// kubeconfig into the shared kubeconfig. It returns the control-plane's
// routable VM IP.
//
// If any step fails partway through, every node booted during this call is
// deleted before returning: without this, a failure after the control-plane
// (or an early worker) comes up would otherwise leave it running indefinitely,
// consuming CPU/memory/disk, and would make the next `create` attempt fail
// with a confusing "already exists" rather than a clean retry.
func (m *Manager) createCluster(clusterName, contextName, image, networkName string, opts *CreateOptions) (cpIP string, err error) {
	cpNodeName := clusterName + "-control-plane"

	existingNodes, err := m.nodeNamesForCluster(clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to check for existing nodes: %w", err)
	}
	if len(existingNodes) > 0 {
		if !opts.Recreate {
			return "", fmt.Errorf("cluster %s already exists, use --recreate to overwrite", clusterName)
		}
		if !confirmRecreation(clusterName) {
			return "", fmt.Errorf("cluster creation cancelled")
		}

		logger.Infof("deleting existing cluster %s", clusterName)
		if err := m.client.Delete(existingNodes...); err != nil {
			return "", fmt.Errorf("failed to delete existing cluster %s: %w", clusterName, err)
		}
	}

	var createdNodes []string
	defer func() {
		if err != nil && len(createdNodes) > 0 {
			logger.Warnf("cluster %s failed to come up, cleaning up %d node(s): %v", clusterName, len(createdNodes), createdNodes)
			if delErr := m.client.Delete(createdNodes...); delErr != nil {
				logger.Warnf("failed to clean up nodes for cluster %s, they may need manual removal: %v", clusterName, delErr)
			}
		}
	}()

	token, err := generateClusterToken()
	if err != nil {
		return "", err
	}

	status := logger.NewStatus()
	status.Start(fmt.Sprintf("booting control-plane node %s", cpNodeName))
	if err := m.client.Run(RunOptions{
		Name:    cpNodeName,
		Network: networkName,
		CPUs:    config.NativeCPU,
		Memory:  config.NativeMemory,
		Image:   image,
		// the node's own container name is used as its --tls-san: the
		// routable VM IP isn't known until after boot, so it can't be baked
		// into the cert as a SAN up front (see createCluster's server URL
		// rewrite below)
		Args: controlPlaneArgs(token, cpNodeName, cpNodeName),
	}); err != nil {
		status.End(false)
		return "", fmt.Errorf("failed to boot control-plane node: %w", err)
	}
	createdNodes = append(createdNodes, cpNodeName)
	if err := makeRootMountShared(m.client, cpNodeName); err != nil {
		status.End(false)
		return "", err
	}
	status.End(true)

	cpIP, err = m.waitForNodeIP(cpNodeName, networkName, 2*time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to determine control-plane IP: %w", err)
	}

	status2 := logger.NewStatus()
	status2.Start(fmt.Sprintf("waiting for k3s on %s to become ready", cpNodeName))
	if err := waitForInVMReady(m.client, cpNodeName, 3*time.Minute); err != nil {
		status2.End(false)
		return "", err
	}
	status2.End(true)

	// worker join is gated by the in-VM readiness check above, not by host
	// reachability - workers join the control-plane over the shared project
	// network directly, without going through the host at all
	serverURL := fmt.Sprintf("https://%s:%d", cpIP, k3sAPIPort)
	for j := 1; j <= opts.NodeCount; j++ {
		workerName := fmt.Sprintf("%s-worker-%d", clusterName, j)

		statusW := logger.NewStatus()
		statusW.Start(fmt.Sprintf("booting worker node %s", workerName))
		if err := m.client.Run(RunOptions{
			Name:    workerName,
			Network: networkName,
			CPUs:    config.NativeCPU,
			Memory:  config.NativeMemory,
			Image:   image,
			Args:    workerArgs(serverURL, token, workerName),
		}); err != nil {
			statusW.End(false)
			return "", fmt.Errorf("failed to boot worker node %s: %w", workerName, err)
		}
		createdNodes = append(createdNodes, workerName)
		if err := makeRootMountShared(m.client, workerName); err != nil {
			statusW.End(false)
			return "", err
		}
		statusW.End(true)
	}

	if err := waitForHostReachable(cpIP, k3sAPIPort, 2*time.Minute); err != nil {
		return "", fmt.Errorf("control-plane not reachable from host: %w", err)
	}

	status3 := logger.NewStatus()
	status3.Start(fmt.Sprintf("configuring kubeconfig for cluster %s", clusterName))
	if err := m.mergeClusterKubeconfig(clusterName, contextName, cpNodeName, cpIP); err != nil {
		status3.End(false)
		return "", err
	}
	status3.End(true)

	// wait for every node to register before returning, so Cilium's own
	// readiness wait (which just polls DaemonSet desired == ready) doesn't
	// converge early against a control-plane-only node set while workers are
	// still joining
	if err := m.waitForNodeCount(contextName, opts.NodeCount+1, 5*time.Minute); err != nil {
		return "", err
	}

	return cpIP, nil
}

// waitForNodeCount waits until at least expected nodes have registered with
// the cluster's API server (registration only, not the Ready condition -
// nodes stay NotReady until Cilium is installed)
func (m *Manager) waitForNodeCount(contextName string, expected int, timeout time.Duration) error {
	clientManager, err := k8s.NewClientManagerForContext(contextName)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client manager: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		listCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		nodes, err := clientManager.GetClientset().CoreV1().Nodes().List(listCtx, metav1.ListOptions{})
		cancel()
		if err != nil {
			logger.Debugf("waitForNodeCount: list nodes failed: %v", err)
		} else {
			logger.Debugf("waitForNodeCount: %d/%d nodes registered", len(nodes.Items), expected)
			if len(nodes.Items) >= expected {
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("timed out waiting for %d nodes to register in cluster %s", expected, contextName)
}

// mergeClusterKubeconfig extracts the control-plane's kubeconfig and merges
// it into the shared kubeconfig under contextName, rewriting the server URL
// to the control-plane's routable VM IP (mirroring kind's own
// updateClusterContext). insecureSkipTLSVerify is required here: the
// control-plane's IP is only known after boot, so it can't have been part of
// the certificate's SAN list generated at first start.
func (m *Manager) mergeClusterKubeconfig(clusterName, contextName, cpNodeName, cpIP string) error {
	raw, err := m.client.Exec(cpNodeName, "cat", "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return fmt.Errorf("failed to extract kubeconfig from %s: %w", cpNodeName, err)
	}

	if err := mergeKubeconfig(clusterName, []byte(raw)); err != nil {
		return fmt.Errorf("failed to merge kubeconfig: %w", err)
	}

	// clusterName and contextName are equal for single-cluster projects (both
	// are just the project name), so the rename would be a no-op
	if clusterName != contextName {
		if err := k8s.RenameContext(clusterName, contextName); err != nil {
			return fmt.Errorf("failed to rename context: %w", err)
		}
	}

	serverURL := fmt.Sprintf("https://%s:%d", cpIP, k3sAPIPort)
	if err := k8s.UpdateClusterServer(clusterName, serverURL, true); err != nil {
		return fmt.Errorf("failed to update cluster server URL: %w", err)
	}

	if err := clearClusterCA(clusterName); err != nil {
		return fmt.Errorf("failed to clear cluster CA data: %w", err)
	}

	return nil
}

// waitForNodeIP polls a node until it has been assigned a routable IPv4
// address on the given network
func (m *Manager) waitForNodeIP(nodeName, networkName string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ip, err := m.client.IPv4Address(nodeName, networkName)
		if err == nil && ip != "" {
			return ip, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}

	return "", fmt.Errorf("timed out waiting for node %s to get an IP address: %w", nodeName, lastErr)
}

// printClusterStatusTable builds and prints a status table for the given project's native clusters
func (m *Manager) printClusterStatusTable(opts *StatusOptions) error {
	networkName := projectNetworkName(opts.Project)

	type clusterStatus struct {
		clusterName string
		contextName string
		status      string
		ip          string
	}

	var statuses []clusterStatus

	for i := 1; i <= opts.NumClusters; i++ {
		clusterName, contextName := clusterAndContextName(opts.Project, opts.NumClusters, i)

		cpNodeName := clusterName + "-control-plane"

		state, err := m.client.State(cpNodeName)
		if err != nil {
			statuses = append(statuses, clusterStatus{clusterName, contextName, "Not Found", "N/A"})
			continue
		}

		ip := "N/A"
		if cpIP, err := m.client.IPv4Address(cpNodeName, networkName); err == nil {
			ip = cpIP
		}

		status := m.clusterReadyStatus(state, contextName)
		statuses = append(statuses, clusterStatus{clusterName, contextName, status, ip})
	}

	fmt.Printf("\nProject: %s\n\n", opts.Project)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CLUSTER\tCONTEXT\tSTATUS\tIP")
	fmt.Fprintln(w, "-------\t-------\t------\t---")

	for _, s := range statuses {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.clusterName, s.contextName, s.status, s.ip)
	}

	w.Flush()
	return nil
}

// clusterReadyStatus determines a human-readable readiness status for a cluster
func (m *Manager) clusterReadyStatus(nodeState, contextName string) string {
	if nodeState != "running" {
		return "Not Running"
	}

	clientManager, err := k8s.NewClientManagerForContext(contextName)
	if err != nil {
		return "Not Ready (kubeconfig issue)"
	}

	nodes, err := clientManager.GetClientset().CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return "Not Ready (API server not responding)"
	}
	if len(nodes.Items) == 0 {
		return "Not Ready (no nodes found)"
	}

	for _, node := range nodes.Items {
		nodeReady := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				nodeReady = true
				break
			}
		}
		if !nodeReady {
			return "Not Ready (nodes not ready)"
		}
	}

	return "Running"
}

// confirmRecreation prompts the user to confirm cluster recreation
func confirmRecreation(clusterName string) bool {
	fmt.Printf("⚠️ cluster '%s' already exists and will be deleted and recreated.\n", clusterName)
	fmt.Print("Are you sure you want to proceed? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		logger.Errorf("failed to read user input: %v", err)
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}
