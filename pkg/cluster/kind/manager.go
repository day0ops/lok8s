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

package kind

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/services"
	"github.com/day0ops/lok8s/pkg/util/docker"
	"github.com/day0ops/lok8s/pkg/util/helm"
	"github.com/day0ops/lok8s/pkg/util/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/kind/pkg/cluster"
)

// Manager manages kind clusters
type Manager struct {
	provider             *cluster.Provider
	helmManager          *helm.HelmManager
	metallbManager       *services.MetalLBManager
	ciliumManager        *services.CiliumManager
	cloudProviderManager *services.CloudProviderKindManager
}

// CreateOptions contains options for creating kind clusters
type CreateOptions struct {
	Project                  string
	GatewayIP                string
	SubnetCIDR               string
	NumClusters              int
	NodeCount                int
	K8sVersion               string
	InstallMetalLB           bool
	InstallCloudProvider     bool
	CNI                      string
	ContainerRuntime         string
	PreferredContainerEngine string
	Recreate                 bool
}

// DeleteOptions contains options for deleting kind clusters
type DeleteOptions struct {
	Project     string
	NumClusters int
	Force       bool
}

// StatusOptions contains options for checking kind cluster status
type StatusOptions struct {
	Project     string
	NumClusters int
}

// LoadImageOptions contains options for loading images into kind clusters
type LoadImageOptions struct {
	Project     string
	Image       string
	NumClusters int
}

// getAvailablePortPrefix finds an available port prefix in the 70XX range, if not search for an available port
func getAvailablePortPrefix(clusterIndex int) (string, error) {
	// try the preferred port first (70XX where XX is cluster index)
	preferredPort := config.KindControlPlanePort + clusterIndex
	if isPortAvailable(preferredPort) {
		return fmt.Sprintf("%d", preferredPort), nil
	}

	// if preferred port is not available, find any available port in 29000 - 30100 range
	for port := 29000; port <= 30100; port++ {
		if isPortAvailable(port) {
			return fmt.Sprintf("%d", port), nil
		}
	}

	return "", errors.New("no available ports found in range 29000 - 30100")
}

// isPortAvailable checks if a port is available for binding
func isPortAvailable(port int) bool {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// getAvailableRegistryPort returns an available registry port.
// Tries 5000 first, then finds a random available port above 30000.
func getAvailableRegistryPort() (int, error) {
	// Try the default port first
	if isPortAvailable(config.KindRegistryPort) {
		return config.KindRegistryPort, nil
	}

	// Find an available port above 30000
	for port := 30000; port <= 65535; port++ {
		if isPortAvailable(port) {
			return port, nil
		}
	}

	return 0, errors.New("no available ports found above 30000")
}

// NewManager creates a new kind manager
func NewManager() *Manager {
	k8sConfigPath, _ := k8s.GetKubeConfigPath()
	helmManager := helm.NewHelmManager(k8sConfigPath)
	return &Manager{
		provider:             cluster.NewProvider(),
		helmManager:          helmManager,
		metallbManager:       services.NewMetalLBManager(helmManager),
		ciliumManager:        services.NewCiliumManager(helmManager, nil), // kind doesn't need binary manager
		cloudProviderManager: services.NewCloudProviderKindManager(),
	}
}

// CreateClusters creates multiple kind clusters
func (m *Manager) CreateClusters(opts *CreateOptions) error {
	logger.Infof("-----> 📢 creating %d Kind cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	// check prerequisites
	if err := m.checkPrerequisites(opts.PreferredContainerEngine); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// validate load balancer options - MetalLB and cloud-provider-kind cannot coexist
	if err := m.validateLoadBalancerOptions(opts); err != nil {
		return fmt.Errorf("load balancer configuration validation failed: %w", err)
	}

	// get kubernetes version
	kindestNode, err := m.getKindestNodeImage(opts.K8sVersion)
	if err != nil {
		return fmt.Errorf("failed to get kind node image: %w", err)
	}

	// create docker network
	actualGatewayIP, err := m.createDockerNetwork(opts.GatewayIP, opts.SubnetCIDR)
	if err != nil {
		return fmt.Errorf("failed to create Docker network: %w", err)
	}
	// Update gateway IP if it was generated from subnetCIDR
	if actualGatewayIP != opts.GatewayIP {
		opts.GatewayIP = actualGatewayIP
		logger.Debugf("using generated gateway IP %s (from subnet %s)", actualGatewayIP, opts.SubnetCIDR)
	}

	// Get an available registry port once (try 5000, fallback to port above 30000)
	// All clusters will use the same port for registry access
	regPort, err := getAvailableRegistryPort()
	if err != nil {
		logger.Warnf("failed to find available registry port: %v, using default %d", err, config.KindRegistryPort)
		regPort = config.KindRegistryPort
	} else {
		logger.Debugf("using registry port %d for all clusters", regPort)
	}

	// create clusters
	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName, contextName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = "kind1"
			contextName = opts.Project
		} else {
			clusterName = fmt.Sprintf("kind%d", i)
			contextName = fmt.Sprintf("%s-%d", opts.Project, i)
		}

		if err := m.createCluster(clusterName, contextName, kindestNode, opts.NodeCount, i, opts, regPort); err != nil {
			return fmt.Errorf("failed to create cluster %s: %w", clusterName, err)
		}

		if opts.InstallMetalLB {
			// initialize tracking before first cluster configuration
			if i == 1 {
				if err := m.metallbManager.InitializeTracking(opts.Project); err != nil {
					logger.Warnf("failed to initialize MetalLB tracking: %v", err)
				}
			}

			if err := m.metallbManager.InstallMetalLB(contextName); err != nil {
				logger.Errorf("failed to install MetalLB on %s: %v", contextName, err)
			} else {
				// configure MetalLB after installation
				// get cluster IP for kind (using container runtime inspect)
				clusterIP, err := m.getKindClusterIP(clusterName)
				if err != nil {
					logger.Errorf("failed to get Kind cluster IP for %s: %v", clusterName, err)
				} else {
					if err := m.metallbManager.ConfigureMetalLB(contextName, clusterIP, i, opts.NumClusters, opts.Project); err != nil {
						logger.Errorf("failed to configure MetalLB on %s: %v", contextName, err)
					}
				}
			}
		}

		if opts.InstallCloudProvider {
			if err := m.cloudProviderManager.Install(contextName, false); err != nil {
				logger.Errorf("failed to install cloud-provider-kind on %s: %v", contextName, err)
			}
		}

		// install cilium after cluster creation (only if cilium CNI is selected)
		if opts.CNI == "cilium" {
			if err := m.ciliumManager.InstallCilium(contextName); err != nil {
				logger.Errorf("failed to install Cilium on %s: %v", contextName, err)
			}
		}
	}

	logger.Infof("🎉 successfully created %d Kind cluster(s)", opts.NumClusters)
	return nil
}

// DeleteClusters deletes multiple kind clusters
func (m *Manager) DeleteClusters(opts *DeleteOptions) error {
	logger.Infof("-----> 🚨 deleting %d Kind cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName, contextName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = "kind1"
			contextName = opts.Project
		} else {
			clusterName = fmt.Sprintf("kind%d", i)
			contextName = fmt.Sprintf("%s-%d", opts.Project, i)
		}

		status := logger.NewStatus()
		status.Start(fmt.Sprintf("deleting Kind cluster %s", clusterName))
		success := true

		// terminate cloud-provider-kind process if it exists
		if err := m.cloudProviderManager.Terminate(contextName, false); err != nil {
			logger.Warnf("failed to terminate cloud-provider-kind process for context %s: %v", contextName, err)
		}

		if err := m.provider.Delete(clusterName, ""); err != nil {
			success = false
			logger.Errorf("failed to delete cluster %s: %v", clusterName, err)
		}

		// clean up kubeconfig context
		if err := k8s.DeleteContext(contextName); err != nil {
			success = false
			logger.Errorf("failed to delete context %s: %v", contextName, err)
		}

		status.End(success)
	}

	// clean up project configuration file
	configManager := config.NewConfigManager()
	if err := configManager.DeleteConfig(opts.Project); err != nil {
		logger.Warnf("failed to delete project config for %s: %v", opts.Project, err)
	} else {
		logger.Infof("deleted project configuration: %s", opts.Project)
	}

	// Delete kind-registry container if force flag is set
	if opts.Force {
		if err := m.deleteKindRegistry(); err != nil {
			logger.Warnf("failed to delete %s container: %v", config.KindRegistryName, err)
		} else {
			logger.Infof("deleted %s container", config.KindRegistryName)
		}
	}

	logger.Infof("successfully deleted %d Kind cluster(s)", opts.NumClusters)
	return nil
}

// StatusClusters shows the status of kind clusters
func (m *Manager) StatusClusters(opts *StatusOptions) error {
	logger.Infof("-----> 📊 checking status of %d Kind cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	// get list of existing kind clusters
	existingClusters, err := m.provider.List()
	if err != nil {
		return fmt.Errorf("failed to list kind clusters: %w", err)
	}

	// create a map of existing cluster names for quick lookup
	clusterMap := make(map[string]bool)
	for _, clusterName := range existingClusters {
		clusterMap[clusterName] = true
	}

	// prepare table data
	type clusterStatus struct {
		clusterName string
		contextName string
		status      string
		ip          string
	}

	var statuses []clusterStatus

	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName, contextName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = "kind1"
			contextName = opts.Project
		} else {
			clusterName = fmt.Sprintf("kind%d", i)
			contextName = fmt.Sprintf("%s-%d", opts.Project, i)
		}

		// check if cluster exists
		if !clusterMap[clusterName] {
			statuses = append(statuses, clusterStatus{
				clusterName: clusterName,
				contextName: contextName,
				status:      "Not Found",
				ip:          "N/A",
			})
			continue
		}

		// get cluster IP
		ip := "N/A"
		clusterIP, err := m.getKindClusterIP(clusterName)
		if err == nil {
			ip = clusterIP
		}

		// check if cluster is ready by trying to get nodes
		status := "Running"
		clientManager, err := k8s.NewClientManagerForContext(contextName)
		if err != nil {
			status = "Not Ready (kubeconfig issue)"
		} else {
			nodes, err := clientManager.GetClientset().CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
			if err != nil {
				status = "Not Ready (API server not responding)"
			} else if len(nodes.Items) == 0 {
				status = "Not Ready (no nodes found)"
			} else {
				// check if all nodes are ready
				allReady := true
				for _, node := range nodes.Items {
					for _, condition := range node.Status.Conditions {
						if condition.Type == "Ready" && condition.Status != "True" {
							allReady = false
							break
						}
					}
					if !allReady {
						break
					}
				}
				if !allReady {
					status = "Not Ready (nodes not ready)"
				}
			}
		}

		statuses = append(statuses, clusterStatus{
			clusterName: clusterName,
			contextName: contextName,
			status:      status,
			ip:          ip,
		})
	}

	// print table
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

// ListClusters lists all kind clusters using the SDK
func (m *Manager) ListClusters() error {
	logger.Info("📋 Kind clusters:")

	clusters, err := m.provider.List()
	if err != nil {
		return fmt.Errorf("failed to list kind clusters: %w", err)
	}

	if len(clusters) == 0 {
		fmt.Println("No Kind clusters found.")
		return nil
	}

	for _, clusterName := range clusters {
		fmt.Printf("  %s\n", clusterName)
	}

	return nil
}

// LoadImage loads a Docker image into kind clusters
func (m *Manager) LoadImage(opts *LoadImageOptions) error {
	logger.Infof("-----> 📦 loading image %s into %d Kind cluster(s) for project %s <-----", opts.Image, opts.NumClusters, opts.Project)

	// check if kind binary is available
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind binary not found in PATH: %w", err)
	}

	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = "kind1"
		} else {
			clusterName = fmt.Sprintf("kind%d", i)
		}

		// verify cluster exists using SDK
		existingClusters, err := m.provider.List()
		if err != nil {
			return fmt.Errorf("failed to list kind clusters: %w", err)
		}

		clusterExists := false
		for _, existingCluster := range existingClusters {
			if existingCluster == clusterName {
				clusterExists = true
				break
			}
		}

		if !clusterExists {
			logger.Warnf("cluster %s not found, skipping image load", clusterName)
			continue
		}

		status := logger.NewStatus()
		status.Start(fmt.Sprintf("loading image %s into cluster %s (%d/%d)", opts.Image, clusterName, i, opts.NumClusters))

		cmd := exec.Command(kindPath, "load", "docker-image", opts.Image, "--name", clusterName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			status.End(false)
			return fmt.Errorf("failed to load image %s into cluster %s: %w", opts.Image, clusterName, err)
		}

		status.End(true)
		logger.Infof("✓ successfully loaded image %s into cluster %s", opts.Image, clusterName)
	}

	logger.Infof("🎉 successfully loaded image %s into %d Kind cluster(s)", opts.Image, opts.NumClusters)
	return nil
}

// checkPrerequisites checks if required tools are installed and running
func (m *Manager) checkPrerequisites(preferredContainerEngine string) error {
	var containerRuntime string

	// Use preferred container engine if specified, otherwise auto-detect
	if preferredContainerEngine != "" {
		containerRuntime = preferredContainerEngine
		logger.Infof("using preferred container engine: %s", containerRuntime)
	} else {
		return errors.New("unable to detect container runtime")
	}

	// Verify that the container runtime is actually running
	if err := m.verifyContainerRuntimeRunning(containerRuntime); err != nil {
		return fmt.Errorf("container runtime not running: %w", err)
	}

	// Set environment variables for kind if using Podman
	if containerRuntime == "podman" {
		os.Setenv("KIND_EXPERIMENTAL_PODMAN", "true")
		os.Setenv("KIND_EXPERIMENTAL_PODMAN_NETWORK", "kind")
	}

	return nil
}

// verifyContainerRuntimeRunning verifies that the container runtime daemon is actually running
func (m *Manager) verifyContainerRuntimeRunning(runtime string) error {
	logger.Debugf("verifying %s daemon is running", runtime)

	// Use 'info' command to check if daemon is running
	// This will fail if the daemon is not running
	cmd := exec.Command(runtime, "info")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s daemon is not running: %w", runtime, err)
	}

	logger.Debugf("%s daemon is running", runtime)
	return nil
}

// getKindestNodeImage returns the appropriate kind node image for the given Kubernetes version
func (m *Manager) getKindestNodeImage(k8sVersion string) (string, error) {
	if k8sVersion == "stable" {
		// Get the latest version (first one in the map, which should be the highest)
		var latestVersion string
		var latestImage string
		for version, image := range config.KindK8sVersions {
			if latestVersion == "" || version > latestVersion {
				latestVersion = version
				latestImage = image
			}
		}
		if latestImage == "" {
			return "", fmt.Errorf("no Kubernetes versions available")
		}
		return fmt.Sprintf("kindest/node:%s", latestImage), nil
	}

	// Extract minor version (e.g., "1.31" from "1.31.2")
	parts := strings.Split(k8sVersion, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid Kubernetes version format: %s", k8sVersion)
	}
	minor := fmt.Sprintf("%s.%s", parts[0], parts[1])

	if version, exists := config.KindK8sVersions[minor]; exists {
		return fmt.Sprintf("kindest/node:%s", version), nil
	}

	return "", fmt.Errorf("unsupported Kubernetes version: %s", k8sVersion)
}

// createDockerNetwork creates a Docker network for kind clusters
// Returns the actual gateway IP used (may be generated from subnetCIDR)
func (m *Manager) createDockerNetwork(gatewayIP, subnetCIDR string) (string, error) {
	// generate gateway IP from subnetCIDR if subnetCIDR has changed from the default
	actualGatewayIP := gatewayIP
	if subnetCIDR != config.DefaultNetworkSubnetCIDR {
		generatedGatewayIP, err := generateGatewayIPFromSubnet(subnetCIDR)
		if err != nil {
			logger.Warnf("failed to generate gateway IP from subnet %s: %v, using provided gateway IP %s", subnetCIDR, err, gatewayIP)
		} else {
			actualGatewayIP = generatedGatewayIP
			logger.Debugf("generated gateway IP %s from subnet %s", actualGatewayIP, subnetCIDR)
		}
	}

	if err := docker.CreateNetwork(config.KindNetworkName, actualGatewayIP, subnetCIDR); err != nil {
		return "", err
	}

	return actualGatewayIP, nil
}

// generateGatewayIPFromSubnet generates a gateway IP from a subnet CIDR
// The gateway IP is the first IP address in the subnet (network IP + 1)
func generateGatewayIPFromSubnet(subnetCIDR string) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return "", fmt.Errorf("failed to parse subnet CIDR %s: %w", subnetCIDR, err)
	}

	ip := ipNet.IP.To4()
	if ip == nil {
		ip = ipNet.IP // fallback to original if not IPv4
	}

	// gateway is the first IP in the subnet (network IP + 1)
	gateway := make(net.IP, len(ip))
	copy(gateway, ip)
	gateway[len(gateway)-1]++

	return gateway.String(), nil
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

// createCluster creates a single kind cluster
func (m *Manager) createCluster(clusterName, contextName, kindestNode string, nodeCount, clusterIndex int, opts *CreateOptions, regPort int) error {
	// Get available port
	cpPort, err := getAvailablePortPrefix(clusterIndex)
	if err != nil {
		return fmt.Errorf("failed to get available port prefix: %w", err)
	}

	// Create temporary config file (needs registry port for containerd config)
	configPath, err := m.createKindConfig(clusterName, kindestNode, nodeCount, clusterIndex, cpPort, regPort)
	if err != nil {
		return fmt.Errorf("failed to create kind config: %w", err)
	}
	defer os.Remove(configPath)

	// Setup registry mirrors (only for the first cluster to avoid duplicates)
	if clusterIndex == 1 {
		if err := m.setupKindRegistryMirrors(regPort, config.KindRegistryName, config.KindNetworkName); err != nil {
			logger.Warnf("failed to setup registry mirrors: %v", err)
			// Don't fail cluster creation if registry setup fails
		}
	}

	// check if cluster already exists
	clusters, err := m.provider.List()
	if err == nil {
		for _, existingCluster := range clusters {
			if existingCluster == clusterName {
				if opts.Recreate {
					// prompt user for confirmation
					if !confirmRecreation(clusterName) {
						return fmt.Errorf("cluster creation cancelled")
					}

					logger.Infof("deleting existing cluster %s", clusterName)
					if err := m.provider.Delete(clusterName, ""); err != nil {
						logger.Warnf("failed to delete existing cluster %s: %v", clusterName, err)
						// continue anyway, the create might still work
					} else {
						logger.Infof("successfully deleted existing cluster %s", clusterName)
					}
				} else {
					logger.Warnf("⚠️ cluster %s already exists", clusterName)
					logger.Warnf("⚠️ use --recreate flag to delete and recreate existing clusters (DESTRUCTIVE !!!)")
					return fmt.Errorf("cluster %s already exists, use --recreate to overwrite", clusterName)
				}
				break
			}
		}
	}

	// Create the cluster
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("creating Kind cluster %s", clusterName))
	err = m.provider.Create(clusterName, cluster.CreateWithConfigFile(configPath))
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed to create kind cluster: %w", err)
	}
	status.End(true)

	// Rename context
	status2 := logger.NewStatus()
	status2.Start(fmt.Sprintf("renaming context for cluster %s", clusterName))
	if err := k8s.RenameContext(fmt.Sprintf("kind-%s", clusterName), contextName); err != nil {
		status2.End(false)
		return fmt.Errorf("failed to rename context: %w", err)
	}
	status2.End(true)

	// Update cluster context with correct server URL
	if err := m.updateClusterContext(clusterIndex, cpPort); err != nil {
		logger.Warnf("failed to update cluster context: %v", err)
	}

	// remove exclude-from-external-load-balancers label from control plane nodes
	status3 := logger.NewStatus()
	status3.Start("removing exclude-from-external-load-balancers label")
	if err := m.removeExcludeLabelFromControlPlane(contextName); err != nil {
		status3.End(false)
		logger.Warnf("failed to remove exclude-from-external-load-balancers label: %v", err)
	} else {
		status3.End(true)
	}

	return nil
}

// updateClusterContext updates the cluster context with the correct server URL
func (m *Manager) updateClusterContext(clusterIndex int, port string) error {
	// Format cluster number
	number := clusterIndex
	clusterName := fmt.Sprintf("kind-kind%d", number)

	// Set the cluster server URL using Kubernetes SDK
	serverURL := fmt.Sprintf("https://%s:%s", "127.0.0.1", port)
	err := k8s.UpdateClusterServer(clusterName, serverURL, false)
	if err != nil {
		return fmt.Errorf("failed to set cluster server URL: %w", err)
	}

	logger.Debugf("updated cluster context %s with server URL: %s", clusterName, serverURL)
	return nil
}

// createKindConfig creates a kind cluster configuration file
func (m *Manager) createKindConfig(clusterName, kindestNode string, nodeCount, clusterIndex int, cpPort string, regPort int) (string, error) {
	region := getRegion(clusterIndex - 1)
	zone := getZone(clusterIndex - 1)

	clusterConfig := fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:%d"]
      endpoint = ["http://%s:%d"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."docker.io"]
      endpoint = ["http://docker:%d"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."us-docker.pkg.dev"]
      endpoint = ["http://us-docker:%d"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."us-central1-docker.pkg.dev"]
      endpoint = ["http://us-central1-docker:%d"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."quay.io"]
      endpoint = ["http://quay:%d"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."gcr.io"]
      endpoint = ["http://gcr:%d"]
nodes:
  - role: control-plane
    image: %s
    extraPortMappings:
      - containerPort: 6443
        hostPort: %s
    labels:
      ingress-ready: "true"
      topology.kubernetes.io/region: %s
      topology.kubernetes.io/zone: %s
`, regPort, config.KindRegistryName, regPort, regPort, regPort, regPort, regPort, regPort, kindestNode, cpPort, region, zone)

	// Add worker nodes
	for i := 1; i <= nodeCount; i++ {
		clusterConfig += fmt.Sprintf(`  - role: worker
    image: %s
`, kindestNode)
	}

	// Add advanced network configuration
	clusterConfig += `networking:
  disableDefaultCNI: true
  serviceSubnet: "10.255.100.0/24"
  podSubnet: "10.100.0.0/16"
`

	// Write clusterConfig to temporary file
	tmpDir := os.TempDir()
	configPath := filepath.Join(tmpDir, fmt.Sprintf("kind-%s.yaml", clusterName))

	if err := os.WriteFile(configPath, []byte(clusterConfig), 0644); err != nil {
		return "", fmt.Errorf("failed to write kind clusterConfig file: %w", err)
	}

	return configPath, nil
}

// setupKindRegistryMirrors sets up registry mirrors for kind clusters
func (m *Manager) setupKindRegistryMirrors(regPort int, regName, networkName string) error {
	status := logger.NewStatus()
	status.Start("setting up kind registry mirrors")
	defer func() {
		if status != nil {
			status.End(true)
		}
	}()

	// Start the main registry
	regPortStr := fmt.Sprintf("%d", regPort)
	if err := m.createRegistryContainer(regName, networkName, regPortStr); err != nil {
		status.End(false)
		return fmt.Errorf("failed to start registry container: %w", err)
	}

	for cacheName, cacheURL := range config.KindRegistries {
		if err := docker.CreateRegistryMirror(cacheName, cacheURL, networkName, regPortStr); err != nil {
			status.End(false)
			return fmt.Errorf("failed to start registry mirror %s: %w", cacheName, err)
		}
	}

	// Success - status.End(true) will be called by defer
	return nil
}

// createRegistryContainer starts the main registry container (only for Docker)
func (m *Manager) createRegistryContainer(regName, networkName, regPort string) error {
	// Use the internal registry port (5000) for the container port mapping
	internalPort := fmt.Sprintf("%d", config.KindRegistryPort)
	return docker.CreateRegistryContainer(regName, networkName, regPort, internalPort)
}

// getRegion returns a region name based on index
func getRegion(index int) string {
	regions := []string{"us-east1", "us-east2", "us-west1", "us-west2"}
	if index < 0 || index >= len(regions) {
		return regions[0]
	}
	return regions[index]
}

// getZone returns a zone name based on index
func getZone(index int) string {
	zones := []string{"us-east1-a", "us-east2-a", "us-west1-a", "us-west2-a"}
	if index < 0 || index >= len(zones) {
		return zones[0]
	}
	return zones[index]
}

// getKindClusterIP gets the IP address of a kind cluster
func (m *Manager) getKindClusterIP(clusterName string) (string, error) {
	// get the container runtime that was detected during prerequisite checking
	containerRuntime, err := docker.GetContainerRuntime()
	if err != nil {
		return "", fmt.Errorf("failed to get container runtime: %w", err)
	}

	// use container runtime inspect to get the cluster IP
	cmd := exec.Command(containerRuntime, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", clusterName+"-control-plane")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get kind cluster IP for %s: %w", clusterName, err)
	}

	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return "", fmt.Errorf("empty IP address returned for kind cluster %s", clusterName)
	}

	logger.Debugf("Kind cluster IP for %s: %s", clusterName, ip)
	return ip, nil
}

// removeExcludeLabelFromControlPlane removes the exclude-from-external-load-balancers label from control plane nodes
func (m *Manager) removeExcludeLabelFromControlPlane(contextName string) error {
	logger.Debugf("removing exclude-from-external-load-balancers label from control plane nodes in context %s", contextName)

	// create client manager for the context
	clientManager, err := k8s.NewClientManagerForContext(contextName)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client manager: %w", err)
	}

	// get all nodes
	nodes, err := clientManager.GetClientset().CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// find control plane nodes and remove the label
	// we want to be able to provision load balancer since we run workloads on it
	for _, node := range nodes.Items {
		// check if this is a control plane node
		isControlPlane := false
		for _, role := range node.Labels {
			if role == "control-plane" {
				isControlPlane = true
				break
			}
		}

		// also check for the node-role.kubernetes.io/control-plane label
		if node.Labels["node-role.kubernetes.io/control-plane"] == "" && node.Labels["node-role.kubernetes.io/master"] == "" {
			// check if it's a kind control plane node by name pattern
			if strings.Contains(node.Name, "-control-plane") {
				isControlPlane = true
			}
		} else {
			isControlPlane = true
		}

		if isControlPlane {
			// check if the exclude label exists
			if _, exists := node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"]; exists {
				logger.Debugf("removing exclude-from-external-load-balancers label from control plane node: %s", node.Name)

				// remove the label
				delete(node.Labels, "node.kubernetes.io/exclude-from-external-load-balancers")

				// update the node
				_, err := clientManager.GetClientset().CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf("failed to update node %s: %w", node.Name, err)
				}

				logger.Debugf("successfully removed exclude-from-external-load-balancers label from node: %s", node.Name)
			} else {
				logger.Debugf("control plane node %s does not have exclude-from-external-load-balancers label", node.Name)
			}
		}
	}

	logger.Debugf("completed exclude-from-external-load-balancers label removal for context: %s", contextName)
	return nil
}

// validateLoadBalancerOptions validates that MetalLB and cloud-provider-kind are not both enabled
// and sets default to cloud-provider-kind for kind clusters
func (m *Manager) validateLoadBalancerOptions(opts *CreateOptions) error {
	// Check for MetalLB on Darwin and warn about Docker networking limitations
	if opts.InstallMetalLB && config.IsDarwin() {
		logger.Warnf("⚠️ MetalLB on Darwin is not effective due to Docker's networking limitations")
		logger.Warnf("⚠️ Docker on macOS doesn't support proper networking to expose load balancer IPs")
		logger.Warnf("⚠️ automatically switching to cloud-provider-kind for load balancer functionality")

		// Switch to cloud-provider-kind
		opts.InstallMetalLB = false
		opts.InstallCloudProvider = true
	}

	// Default to cloud-provider-kind if neither is explicitly set
	if !opts.InstallMetalLB && !opts.InstallCloudProvider {
		logger.Infof("no load balancer specified, defaulting to cloud-provider-kind for Kind clusters")
		opts.InstallCloudProvider = true
	}

	if opts.InstallMetalLB && opts.InstallCloudProvider {
		return fmt.Errorf("MetalLB and cloud-provider-kind cannot be installed together - they conflict with each other")
	}

	if opts.InstallCloudProvider {
		logger.Infof("cloud-provider-kind will be installed for load balancer functionality")
	} else if opts.InstallMetalLB {
		logger.Infof("MetalLB will be installed for load balancer functionality")
	}

	return nil
}

// deleteKindRegistry deletes the kind-registry container and its associated mirror containers
func (m *Manager) deleteKindRegistry() error {
	// List of registry containers to delete
	registryContainers := []string{
		config.KindRegistryName,
		"docker",
		"us-docker",
		"us-central1-docker",
		"quay",
		"gcr",
	}

	return docker.DeleteRegistryContainers(registryContainers)
}
