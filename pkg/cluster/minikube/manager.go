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

package minikube

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/network"
	"github.com/day0ops/lok8s/pkg/services"
	"github.com/day0ops/lok8s/pkg/util/helm"
	"github.com/day0ops/lok8s/pkg/util/k8s"
	"github.com/day0ops/lok8s/pkg/util/version"
)

// NetworkManager defines the interface for network management operations
type NetworkManager interface {
	PrerequisiteChecks() bool
	EnsureNetwork() error
	DeleteNetwork(force bool) error
}

// Manager manages minikube clusters
type Manager struct {
	binaryManager  *BinaryManager
	helmManager    *helm.HelmManager
	ciliumManager  *services.CiliumManager
	metallbManager *services.MetalLBManager
}

// CreateOptions contains options for creating minikube clusters
type CreateOptions struct {
	Project          string
	Bridge           string
	CPU              string
	Memory           string
	Disk             string
	SubnetCIDR       string
	NumClusters      int
	NodeCount        int
	K8sVersion       string
	InstallMetalLB   bool
	Verbose          bool
	CNI              string
	ContainerRuntime string
}

// DeleteOptions contains options for deleting minikube clusters
type DeleteOptions struct {
	Project     string
	NumClusters int
	Force       bool
	Bridge      string
	SubnetCIDR  string
}

// NewManager creates a new minikube manager
func NewManager() *Manager {
	binaryManager := NewBinaryManager()
	k8sConfigPath, _ := k8s.GetKubeConfigPath()
	helmManager := helm.NewHelmManager(k8sConfigPath)

	return &Manager{
		binaryManager:  binaryManager,
		helmManager:    helmManager,
		ciliumManager:  services.NewCiliumManager(helmManager, binaryManager),
		metallbManager: services.NewMetalLBManagerWithOptions(helmManager, config.MetalLBRangeMinLastOctet, config.MetalLBRangeMaxLastOctet),
	}
}

// CreateClusters creates multiple minikube clusters
func (m *Manager) CreateClusters(opts *CreateOptions) error {
	logger.Infof("-----> 📢 creating %d Minikube cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	// check prerequisites
	if err := m.checkPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// get Kubernetes version
	k8sVersion, err := m.getMinikubeK8sVersion(opts.K8sVersion)
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes version: %w", err)
	}

	// setup network and driver based on OS
	networkManager, driver, err := m.setupNetworkAndDriver(opts.Project, opts.Bridge, opts.SubnetCIDR)
	if err != nil {
		return fmt.Errorf("failed to setup network and driver: %w", err)
	}

	// Extract network name and subnet from the network manager
	var networkName string
	var actualSubnet string
	if net, ok := networkManager.(*network.Network); ok {
		networkName = net.Name
		actualSubnet = net.Subnet
	} else {
		return fmt.Errorf("unexpected network manager type")
	}

	// Update subnet in options if it was changed (e.g., free subnet was selected)
	if actualSubnet != "" && actualSubnet != opts.SubnetCIDR {
		opts.SubnetCIDR = actualSubnet
		logger.Debugf("using subnet %s (updated from %s)", actualSubnet, opts.SubnetCIDR)
	}

	// create clusters
	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = opts.Project
		} else {
			clusterName = fmt.Sprintf("%s-%d", opts.Project, i)
		}

		if err := m.createCluster(clusterName, k8sVersion, driver, opts.CPU, opts.Memory, opts.Disk, networkName, opts.CNI, opts.ContainerRuntime, opts.NodeCount, i, opts.Verbose); err != nil {
			return fmt.Errorf("failed to create cluster %s: %w", clusterName, err)
		}

		if opts.InstallMetalLB {
			// initialize tracking before first cluster configuration
			if i == 1 {
				if err := m.metallbManager.InitializeTracking(opts.Project); err != nil {
					logger.Warnf("failed to initialize MetalLB tracking: %v", err)
				}
			}

			if err := m.metallbManager.InstallMetalLB(clusterName); err != nil {
				logger.Errorf("failed to install MetalLB on %s: %v", clusterName, err)
			}

			// configure MetalLB after installation
			var ipAddress string
			if ipAddress, err = m.getMinikubeIP(clusterName); err != nil {
				logger.Errorf("failed to get Minikube IP for cluster %s: %v", clusterName, err)
			} else {
				if err := m.metallbManager.ConfigureMetalLB(clusterName, ipAddress, i, opts.NumClusters, opts.Project); err != nil {
					logger.Errorf("failed to configure MetalLB on %s: %v", clusterName, err)
				}
			}
		}
	}

	logger.Infof("✓ successfully created %d Minikube cluster(s)", opts.NumClusters)

	// show profile list
	if err := m.showProfileList(); err != nil {
		logger.Warnf("failed to show profile list: %v", err)
	}

	return nil
}

// DeleteClusters deletes multiple minikube clusters
func (m *Manager) DeleteClusters(opts *DeleteOptions) error {
	logger.Infof("-----> 🚨 deleting %d Minikube cluster(s) for project %s <-----", opts.NumClusters, opts.Project)

	// set environment variable to disable styling
	os.Setenv("MINIKUBE_IN_STYLE", "false")

	// get binary path
	binaryPath, err := m.binaryManager.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get minikube binary path: %w", err)
	}

	// use provided Bridge and SubnetCIDR, or defaults if not provided
	bridge := opts.Bridge
	if bridge == "" {
		bridge = config.MinikubeDefaultBridgeNetName
	}
	subnetCIDR := opts.SubnetCIDR
	if subnetCIDR == "" {
		subnetCIDR = config.DefaultNetworkSubnetCIDR
	}

	// setup network and driver based on OS
	networkManager, _, err := m.setupNetworkAndDriver(opts.Project, bridge, subnetCIDR)
	if err != nil {
		return fmt.Errorf("failed to setup network and driver: %w", err)
	}

	// ensure network prerequisites are satisfied (e.g., vmnet-helper is installed on Darwin)
	if networkManager != nil {
		if !networkManager.PrerequisiteChecks() {
			// on Darwin, try to ensure network is set up, which will install vmnet-helper if needed
			if config.IsDarwin() {
				logger.Debugf("prerequisites not satisfied, ensuring network (will install vmnet-helper if needed)")
				if err := networkManager.EnsureNetwork(); err != nil {
					logger.Warnf("failed to ensure network prerequisites: %v", err)
					// don't fail deletion if network setup fails, but log a warning
				}
			} else {
				return fmt.Errorf("not all prerequisites are satisfied")
			}
		}
	}

	for i := 1; i <= opts.NumClusters; i++ {
		var clusterName string
		if opts.NumClusters == 1 {
			// if only one cluster, don't add suffix
			clusterName = opts.Project
		} else {
			clusterName = fmt.Sprintf("%s-%d", opts.Project, i)
		}

		status := logger.NewStatus()
		status.Start(fmt.Sprintf("deleting Minikube cluster %s (%d/%d)", clusterName, i, opts.NumClusters))

		// try deleting with current naming scheme first
		err := m.deleteCluster(binaryPath, clusterName, opts.Force)
		if err != nil {
			// if it fails and we're using the new naming scheme (no suffix), try the old naming scheme for backward compatibility
			if opts.NumClusters == 1 {
				oldClusterName := fmt.Sprintf("%s-%d", opts.Project, i)
				logger.Debugf("cluster %s not found, trying old naming scheme: %s", clusterName, oldClusterName)
				if err2 := m.deleteCluster(binaryPath, oldClusterName, opts.Force); err2 != nil {
					status.End(false)
					logger.Errorf("failed to delete cluster %s or %s: %v / %v", clusterName, oldClusterName, err, err2)
					return fmt.Errorf("failed to delete cluster %s (also tried %s): %w", clusterName, oldClusterName, err)
				}
				// successfully deleted with old naming scheme
				status.End(true)
				continue
			}
			status.End(false)
			logger.Errorf("failed to delete cluster %s: %v", clusterName, err)
			return fmt.Errorf("failed to delete cluster %s: %w", clusterName, err)
		}
		status.End(true)
	}

	// clean up network if network manager is available
	if networkManager != nil && opts.Force {
		if net, ok := networkManager.(*network.Network); ok {
			// Set the network name for deletion on Linux (Darwin already has it set)
			if config.IsLinux() {
				networkName := fmt.Sprintf("%s-net", opts.Project)
				net.Name = networkName
			}
			if err := net.DeleteNetwork(opts.Force); err != nil {
				logger.Warnf("failed to cleanup network: %v", err)
			}
		}
	}

	// clean up project configuration file
	configManager := config.NewConfigManager()
	if err := configManager.DeleteConfig(opts.Project); err != nil {
		logger.Warnf("failed to delete project config for %s: %v", opts.Project, err)
	} else {
		logger.Infof("✓ deleted project configuration: %s", opts.Project)
	}

	logger.Infof("✓ successfully deleted %d Minikube cluster(s)", opts.NumClusters)
	return nil
}

// deleteCluster deletes a single minikube cluster and captures error output
func (m *Manager) deleteCluster(binaryPath, clusterName string, force bool) error {
	args := []string{"delete", "-p", clusterName}
	if force {
		args = append(args, "--purge=true")
	}
	cmd := exec.Command(binaryPath, args...)

	// capture stderr to show actual error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// suppress stdout during deletion since spinner provides feedback
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		cmd.Stdout = devNull
		defer devNull.Close()
	} else {
		// fallback to logger output if DevNull is not available
		cmd.Stdout = logger.GetLogger().Out
	}

	err = cmd.Run()
	if err != nil {
		// include stderr in error message for better debugging
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return err
	}
	return nil
}

// checkPrerequisites checks if required tools are installed
func (m *Manager) checkPrerequisites() error {
	// ensure minikube binary is available
	if err := m.binaryManager.EnsureBinary(); err != nil {
		return fmt.Errorf("minikube binary not available: %w", err)
	}

	// get binary path for version check
	binaryPath, err := m.binaryManager.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get minikube binary path: %w", err)
	}

	// check minikube version
	cmd := exec.Command(binaryPath, "version", "--short")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get minikube version: %w", err)
	}

	currentVersion := strings.TrimSpace(strings.TrimPrefix(string(output), "v"))
	if version.Compare(config.MinikubeMinSupportedVersion, currentVersion) > 0 {
		return fmt.Errorf("minikube version %s is too old. Minimum supported version is %s", currentVersion, config.MinikubeMinSupportedVersion)
	}

	logger.Debugf("using Minikube version: %s", currentVersion)

	// os-specific checks
	if config.IsLinux() {
		return m.checkLinuxPrerequisites()
	} else if config.IsDarwin() {
		return m.checkDarwinPrerequisites()
	}

	return fmt.Errorf("unsupported operating system: %s", config.GetOS())
}

// checkLinuxPrerequisites checks Linux-specific prerequisites
func (m *Manager) checkLinuxPrerequisites() error {
	// check KVM support
	if err := m.checkKVMSupport(); err != nil {
		return fmt.Errorf("KVM support check failed: %w", err)
	}

	// check libvirt
	if err := m.checkLibvirt(); err != nil {
		return fmt.Errorf("libvirt check failed: %w", err)
	}

	return nil
}

// checkDarwinPrerequisites checks darwin-specific prerequisites
func (m *Manager) checkDarwinPrerequisites() error {
	// check vfkit installation
	if err := m.checkVfkitInstalled(); err != nil {
		return err
	}

	logger.Debug("macOS prerequisites check completed")
	return nil
}

// checkKVMSupport checks if KVM is available and loaded
func (m *Manager) checkKVMSupport() error {
	// check if KVM modules are loaded
	cmd := exec.Command("lsmod")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check loaded modules: %w", err)
	}

	if !strings.Contains(string(output), "kvm") {
		return fmt.Errorf("KVM modules not loaded. Please ensure virtualization is enabled")
	}

	return nil
}

// checkLibvirt checks if libvirt is properly installed and running
func (m *Manager) checkLibvirt() error {
	// check if virsh is available
	if err := exec.Command("virsh", "--version").Run(); err != nil {
		return fmt.Errorf("virsh not found. Please install libvirt")
	}

	// check if libvirtd is running
	cmd := exec.Command("systemctl", "is-active", "--quiet", "libvirtd")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("libvirtd is not running. Please start it with: systemctl start libvirtd")
	}

	// check if user is in libvirt group
	cmd = exec.Command("id", "-nG")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check user groups: %w", err)
	}

	if !strings.Contains(string(output), "libvirt") {
		username := os.Getenv("USER")
		return fmt.Errorf("user %s is not in the libvirt group. Add with: sudo usermod -aG libvirt %s", username, username)
	}

	return nil
}

// checkVfkitInstalled checks if vfkit is installed and meets minimum version requirements
func (m *Manager) checkVfkitInstalled() error {
	// check if vfkit is available
	if err := exec.Command("vfkit", "--version").Run(); err != nil {
		logger.Infof("vfkit not found, attempting to install via Homebrew...")

		// check if brew is available
		if err := exec.Command("brew", "--version").Run(); err != nil {
			return fmt.Errorf("vfkit not found and Homebrew is not available. Please install Homebrew first, then run: 'brew install vfkit'")
		}

		// install vfkit via brew
		logger.Infof("installing vfkit via Homebrew...")
		cmd := exec.Command("brew", "install", "vfkit", "-q")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install vfkit via Homebrew: %w", err)
		}

		logger.Infof("✓ vfkit installed successfully via Homebrew")
	}

	// get vfkit version
	cmd := exec.Command("vfkit", "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get vfkit version: %w", err)
	}

	// parse version from output (e.g., "vfkit version 0.6.1")
	versionStr := strings.TrimSpace(string(output))
	parts := strings.Fields(versionStr)
	if len(parts) < 3 {
		return fmt.Errorf("unable to parse vfkit version from: %s", versionStr)
	}

	installedVersion := parts[2]
	if version.Compare(config.VfkitMinSupportedVersion, installedVersion) > 0 {
		return fmt.Errorf("vfkit version %s is too old. Required: %s or higher", installedVersion, config.VfkitMinSupportedVersion)
	}

	logger.Debugf("using vfkit version: %s", installedVersion)
	return nil
}

// getMinikubeK8sVersion returns the appropriate Kubernetes version for minikube
func (m *Manager) getMinikubeK8sVersion(k8sVersion string) (string, error) {
	if k8sVersion == "stable" {
		// get the latest version
		for _, version := range config.MinikubeK8sVersions {
			return fmt.Sprintf("v%s", version), nil
		}
	}

	// extract minor version (e.g., "1.31" from "1.31.2")
	parts := strings.Split(k8sVersion, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid Kubernetes version format: %s", k8sVersion)
	}
	minor := fmt.Sprintf("%s.%s", parts[0], parts[1])

	if version, exists := config.MinikubeK8sVersions[minor]; exists {
		return fmt.Sprintf("v%s", version), nil
	}

	// if not in our predefined versions, validate it's a proper semver and use it
	if version.IsValidSemver(k8sVersion) {
		return fmt.Sprintf("v%s", k8sVersion), nil
	}

	return "", fmt.Errorf("unsupported Kubernetes version: %s", k8sVersion)
}

// setupNetworkAndDriver sets up networking and determines the appropriate driver
// Returns: NetworkManager, driver, error
func (m *Manager) setupNetworkAndDriver(project, bridge, subnetCIDR string) (NetworkManager, string, error) {
	if config.IsLinux() {
		// create libvirt network
		networkName := fmt.Sprintf("%s-net", project)
		libvirtNet := &network.Network{
			Name:          networkName,
			Bridge:        bridge,
			Subnet:        subnetCIDR,
			ConnectionURI: config.MinikubeQemuSystem,
		}

		var networkManager NetworkManager = libvirtNet
		if err := networkManager.EnsureNetwork(); err != nil {
			return nil, "", fmt.Errorf("failed to create libvirt network: %w", err)
		}

		// use kvm2 driver in linux
		return networkManager, "kvm2", nil
	} else if config.IsDarwin() {
		// check darwin-specific prerequisites
		vmnetNetwork := &network.Network{
			Name: config.MinikubeVmnetNetworkName,
		}
		var vmnetManager NetworkManager = vmnetNetwork
		if err := vmnetManager.EnsureNetwork(); err != nil {
			return nil, "", fmt.Errorf("vmnet-helper installation failed: %w", err)
		}

		// use vfkit driver for darwin
		return vmnetManager, "vfkit", nil
	}

	return nil, "", fmt.Errorf("unsupported operating system: %s", config.GetOS())
}

// createCluster creates a single minikube cluster
func (m *Manager) createCluster(clusterName, k8sVersion, driver, cpu, memory, disk, networkName, cni, containerRuntime string, nodeCount, clusterIndex int, verbose bool) error {
	// set environment variable to disable styling
	os.Setenv("MINIKUBE_IN_STYLE", "false")

	// get binary path
	binaryPath, err := m.binaryManager.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get minikube binary path: %w", err)
	}

	region := getRegion(clusterIndex - 1)
	zone := getZone(clusterIndex - 1)

	// determine the actual CNI to use for minikube
	minikubeCNI := cni
	if cni == "cilium" {
		// generate Cilium manifest file from helm chart
		manifestPath, err := m.ciliumManager.GenerateCiliumManifest(clusterName)
		if err != nil {
			return fmt.Errorf("failed to generate Cilium manifest: %w", err)
		}
		// use the manifest file path for --cni flag
		minikubeCNI = manifestPath
	}

	args := []string{
		"start",
		"-p", clusterName,
		"--kubernetes-version=" + k8sVersion,
		"--driver=" + driver,
		"--container-runtime=" + containerRuntime,
		"--cni=" + minikubeCNI,
		"--cpus=" + cpu,
		"--memory=" + memory,
		"--disk-size=" + disk,
		"--network=" + networkName,
		"--nodes=" + strconv.Itoa(nodeCount),
		"--service-cluster-ip-range=" + config.GetMinikubeServiceIPRange(clusterIndex),
		"--extra-config=kubelet.node-labels=topology.kubernetes.io/region=" + region + ",topology.kubernetes.io/zone=" + zone,
	}

	// add verbose flag if requested
	if verbose {
		args = append(args, "--alsologtostderr")
		args = append(args, "--v=7")
	}

	status := logger.NewStatus()
	status.Start(fmt.Sprintf("creating Minikube cluster %s", clusterName))

	cmd := exec.Command(binaryPath, args...)
	// Redirect minikube output through the logger so it properly clears the spinner line
	cmd.Stdout = logger.GetLogger().Out
	cmd.Stderr = logger.GetLogger().Out

	if err := cmd.Run(); err != nil {
		status.End(false)
		return fmt.Errorf("failed to start minikube cluster: %w", err)
	}

	// wait for all nodes to be ready
	if err := m.waitForNodesReady(clusterName); err != nil {
		status.End(false)
		return fmt.Errorf("nodes not ready: %w", err)
	}
	status.End(true)

	return nil
}

// waitForNodesReady waits for all nodes in the cluster to be ready
func (m *Manager) waitForNodesReady(clusterName string) error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("waiting for nodes to be ready in cluster %s", clusterName))
	defer status.End(true)

	logger.Debug("waiting for nodes to be ready...")

	// create client manager for the cluster
	clientManager, err := k8s.NewClientManagerForContext(clusterName)
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed to create kubernetes client manager: %w", err)
	}

	// wait for nodes to be ready with 5 minute timeout
	timeout := 5 * time.Minute
	if err := clientManager.WaitForNodesReady(timeout); err != nil {
		status.End(false)
		return err
	}

	return nil
}

// showProfileList displays the current minikube profiles
func (m *Manager) showProfileList() error {
	// get binary path
	binaryPath, err := m.binaryManager.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get minikube binary path: %w", err)
	}

	logger.Info("📋 Minikube profiles:")

	cmd := exec.Command(binaryPath, "profile", "list")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to list minikube profiles: %w", err)
	}

	return nil
}

// getMinikubeIP gets the IP address of a minikube cluster
func (m *Manager) getMinikubeIP(clusterName string) (string, error) {
	cmd := exec.Command("minikube", "ip", "-p", clusterName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get minikube IP for cluster %s: %w", clusterName, err)
	}

	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return "", fmt.Errorf("empty IP address returned for cluster %s", clusterName)
	}

	logger.Debugf("Minikube IP for cluster %s: %s", clusterName, ip)
	return ip, nil
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
