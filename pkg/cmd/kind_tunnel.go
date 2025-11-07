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

package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/services"
)

// kindTunnelCmd manages cloud-provider-kind processes for darwin
func kindTunnelCmd() *cobra.Command {
	var (
		project   string
		terminate bool
		showPorts bool
		format    string
	)

	cmd := &cobra.Command{
		Use:   "kind-tunnel",
		Short: "Manage cloud-provider-kind processes (requires sudo on macOS)",
		Long: `Manage cloud-provider-kind background processes for Kind clusters on macOS and Linux.
On macOS, this command requires sudo access to manage Docker privileged ports.
On Linux, sudo is not required.

Use this command to:
- Start cloud-provider-kind processes for existing Kind clusters
- Kill existing cloud-provider-kind processes
- Display ephemeral ports created by Docker/Podman for Envoy load balancers`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Only require sudo on macOS/Darwin
			if config.IsDarwin() && syscall.Geteuid() != 0 {
				return fmt.Errorf("this command must be run as sudo on macOS")
			}

			if project == "" {
				return fmt.Errorf("project name is required")
			}

			// load saved config to get environment and other settings
			savedConfig, err := configManager.LoadConfig(project)
			if err != nil {
				return fmt.Errorf("failed to load project config: %w", err)
			}

			// check if this is a kind project
			if savedConfig == nil || savedConfig.Environment != "kind" {
				return fmt.Errorf("project %s is not configured for kind environment", project)
			}

			if showPorts {
				return showLoadBalancerPorts(project, savedConfig.NumClusters, format)
			} else if terminate {
				return terminateCloudProviderProcesses(project)
			} else {
				return startCloudProviderProcesses(project)
			}
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Project name (required)")
	cmd.Flags().BoolVarP(&terminate, "terminate", "t", false, "Terminate existing cloud-provider-kind processes under the given project")
	cmd.Flags().BoolVarP(&showPorts, "ports", "s", false, "Show ephemeral ports created by Docker/Podman for the provisioned load balancers")
	cmd.Flags().StringVarP(&format, "format", "f", "table", "Output format for port display (table, json)")

	if err := cmd.MarkFlagRequired("project"); err != nil {
		logger.Warnf("failed to mark project flag as required: %v", err)
	}

	return cmd
}

// startCloudProviderProcesses starts cloud-provider-kind processes for the specified project
func startCloudProviderProcesses(project string) error {
	logger.Infof("starting cloud-provider-kind processes for project %s", project)

	clusterIndex := 1

	// load saved config to get number of clusters
	savedConfig, err := configManager.LoadConfig(project)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	if savedConfig == nil {
		return fmt.Errorf("project %s not found", project)
	}

	cloudProviderManager := services.NewCloudProviderKindManager()

	// start cloud-provider-kind for each cluster
	var contextName string
	if savedConfig.NumClusters == 1 {
		// if only one cluster, don't add suffix
		contextName = project
	} else {
		contextName = fmt.Sprintf("%s-%d", project, clusterIndex)
	}

	logger.Infof("installing cloud-provider-kind for context %s", contextName)

	// ensure the correct context is set before starting cloud-provider-kind
	if err := setKubeContext(contextName); err != nil {
		logger.Errorf("failed to set kube context %s: %v", contextName, err)
	}

	if err := cloudProviderManager.Install(contextName, true); err != nil {
		logger.Errorf("failed to install cloud-provider-kind for context %s: %v", contextName, err)
		// continue with other clusters even if one fails
	} else {
		logger.Infof("✓ successfully started cloud-provider-kind for context %s", contextName)
	}

	logger.Infof("🎉 cloud-provider-kind processes started for project %s", project)

	// automatically show ports after starting processes
	return showLoadBalancerPorts(project, savedConfig.NumClusters, "table")
}

// terminateCloudProviderProcesses terminates cloud-provider-kind processes for the specified project
func terminateCloudProviderProcesses(project string) error {
	logger.Infof("terminating cloud-provider-kind processes for project %s", project)

	// load saved config to get number of clusters
	savedConfig, err := configManager.LoadConfig(project)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	if savedConfig == nil {
		return fmt.Errorf("project %s not found", project)
	}

	cloudProviderManager := services.NewCloudProviderKindManager()
	clusterIndex := 1

	var contextName string
	if savedConfig.NumClusters == 1 {
		// if only one cluster, don't add suffix
		contextName = project
	} else {
		contextName = fmt.Sprintf("%s-%d", project, clusterIndex)
	}

	logger.Infof("terminating cloud-provider-kind for context %s", contextName)

	if err := cloudProviderManager.Terminate(contextName, true); err != nil {
		logger.Warnf("failed to terminate cloud-provider-kind for context %s: %v", contextName, err)
		// continue with other clusters even if one fails
	} else {
		logger.Infof("✓ successfully terminated cloud-provider-kind for context %s", contextName)
	}

	logger.Infof("🎉 cloud-provider-kind processes terminated for project %s", project)
	return nil
}

// setKubeContext sets the current kubernetes context
func setKubeContext(contextName string) error {
	logger.Debugf("setting kube context to %s", contextName)

	// use kubectl to set the context
	cmd := exec.Command("kubectl", "config", "use-context", contextName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set kube context %s: %w, output: %s", contextName, err, string(output))
	}

	logger.Debugf("successfully set kube context to %s", contextName)
	return nil
}

// LoadBalancerPortInfo represents port information for load balancer containers
type LoadBalancerPortInfo struct {
	ClusterName      string
	LoadBalancerName string
	HostPort         string
	ServicePort      string
	Protocol         string
	IPVersion        string
	URL              string
}

// showLoadBalancerPorts displays ephemeral ports created by Docker/Podman for load balancers
func showLoadBalancerPorts(project string, numClusters int, format string) error {
	logger.Infof("showing load balancer ports for project %s (%d clusters)", project, numClusters)

	// validate format
	if format != "table" && format != "json" {
		return fmt.Errorf("invalid format '%s'. Supported formats: table, json", format)
	}

	// set default format if not specified
	if format == "" {
		format = "table"
	}

	// get host IP (non-loopback)
	hostIP, err := getHostIP()
	if err != nil {
		logger.Warnf("failed to get host IP: %v", err)
		hostIP = "localhost"
	}

	portInfos := []LoadBalancerPortInfo{}

	for i := 1; i <= numClusters; i++ {
		clusterName := fmt.Sprintf("kind%d", i)

		// get load balancer containers for this cluster
		containers, err := getLoadBalancerContainers(clusterName)
		if err != nil {
			logger.Warnf("failed to get load balancer containers for cluster %s: %v", clusterName, err)
			continue
		}

		for _, container := range containers {
			ports := parsePortMappings(container.Ports)
			for _, port := range ports {
				portInfos = append(portInfos, LoadBalancerPortInfo{
					ClusterName:      clusterName,
					LoadBalancerName: container.LoadBalancerName,
					HostPort:         port.HostPort,
					ServicePort:      port.ServicePort,
					Protocol:         port.Protocol,
					IPVersion:        port.IPVersion,
					URL:              generateURL(hostIP, port.HostPort),
				})
			}
		}
	}

	// deduplicate port entries (ignore IP family)
	portInfos = deduplicatePorts(portInfos)

	// display ports based on format
	switch format {
	case "table":
		if len(portInfos) > 0 {
			displayPortsTable(portInfos, hostIP)
		} else {
			fmt.Printf("\n🌐 Host IP: %s\n", hostIP)
			fmt.Println("No load balancers found. Make sure cloud-provider-kind is running.")
		}
	case "json":
		displayPortsJSON(portInfos, hostIP)
	}

	return nil
}

// deduplicatePorts removes duplicate port entries, keeping only one entry per unique combination
// of cluster, load balancer, host port, service port, and protocol (ignoring IP family)
func deduplicatePorts(portInfos []LoadBalancerPortInfo) []LoadBalancerPortInfo {
	seen := make(map[string]bool)
	var deduplicated []LoadBalancerPortInfo

	for _, info := range portInfos {
		// create a key that ignores IP family
		key := fmt.Sprintf("%s:%s:%s:%s:%s",
			info.ClusterName,
			info.LoadBalancerName,
			info.HostPort,
			info.ServicePort,
			info.Protocol)

		if !seen[key] {
			seen[key] = true
			deduplicated = append(deduplicated, info)
		}
	}

	return deduplicated
}

// generateURL creates a full URL from host IP, port, and protocol
func generateURL(hostIP, port string) string {
	// determine protocol based on port
	var scheme string
	if port == "443" {
		scheme = "https://"
	} else {
		scheme = "http://"
	}

	return fmt.Sprintf("%s%s:%s", scheme, hostIP, port)
}

// displayPortsTable displays port information in table format
func displayPortsTable(portInfos []LoadBalancerPortInfo, hostIP string) {
	fmt.Printf("\n🌐 Host IP: %s\n", hostIP)
	fmt.Println("┌─────────────────┬─────────────────────┬────────────┬───────────────┬──────────┬─────────────────────────────┐")
	fmt.Println("│ Cluster         │ Load Balancer       │ Host Port  │ Service Port  │ Protocol │ URL                         │")
	fmt.Println("├─────────────────┼─────────────────────┼────────────┼───────────────┼──────────┼─────────────────────────────┤")

	for _, info := range portInfos {
		fmt.Printf("│ %-15s │ %-19s │ %-10s │ %-13s │ %-8s │ %-27s │\n",
			info.ClusterName,
			info.LoadBalancerName,
			info.HostPort,
			info.ServicePort,
			info.Protocol,
			info.URL,
		)
	}

	fmt.Println("└─────────────────┴─────────────────────┴────────────┴───────────────┴──────────┴─────────────────────────────┘")
}

// displayPortsJSON displays port information in JSON format
func displayPortsJSON(portInfos []LoadBalancerPortInfo, hostIP string) {
	type PortOutput struct {
		HostIP string                 `json:"host_ip"`
		Ports  []LoadBalancerPortInfo `json:"ports"`
	}

	output := PortOutput{
		HostIP: hostIP,
		Ports:  portInfos,
	}

	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		logger.Errorf("failed to marshal JSON: %v", err)
		return
	}

	fmt.Println(string(jsonData))
}

// DockerContainer represents a Docker container from docker ps output
type DockerContainer struct {
	ID               string `json:"ID"`
	Image            string `json:"Image"`
	Labels           string `json:"Labels"`
	Names            string `json:"Names"`
	Ports            string `json:"Ports"`
	State            string `json:"State"`
	LoadBalancerName string // Extracted from labels
}

// PortMapping represents a parsed port mapping
type PortMapping struct {
	HostPort    string
	ServicePort string
	Protocol    string
	IPVersion   string
}

// getHostIP gets the non-loopback IP address
func getHostIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
	}

	return "localhost", nil
}

// retryWithTimeout executes a function with retry logic and timeout
func retryWithTimeout(operation func() (interface{}, error), timeout time.Duration, retryInterval time.Duration, operationName string) (interface{}, error) {
	startTime := time.Now()

	for {
		result, err := operation()
		if err != nil {
			return nil, err
		}

		// if we got a result, return it
		if result != nil {
			return result, nil
		}

		// check if we've exceeded the timeout
		if time.Since(startTime) > timeout {
			logger.Warnf("timeout waiting for %s after %v", operationName, timeout)
			return nil, nil // return nil instead of error
		}

		// wait before retrying
		logger.Debugf("no result for %s, retrying in %v...", operationName, retryInterval)
		time.Sleep(retryInterval)
	}
}

// getLoadBalancerContainers gets load balancer containers for a specific cluster
func getLoadBalancerContainers(clusterName string) ([]DockerContainer, error) {
	timeout := 60 * time.Second
	retryInterval := 2 * time.Second

	operation := func() (interface{}, error) {
		cmd := exec.Command("docker", "ps", "--filter", "label=io.x-k8s.cloud-provider-kind.cluster", "--format", "json")
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to run docker ps: %w", err)
		}

		var containers []DockerContainer
		lines := strings.Split(string(output), "\n")

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			var container DockerContainer
			if err := json.Unmarshal([]byte(line), &container); err != nil {
				continue
			}

			// check if this is a load balancer container for our cluster
			if strings.Contains(container.Labels, fmt.Sprintf("io.x-k8s.cloud-provider-kind.cluster=%s", clusterName)) &&
				strings.Contains(container.Image, "envoy") &&
				container.State == "running" {

				// extract load balancer name from labels
				loadBalancerName := extractLoadBalancerName(container.Labels)
				container.LoadBalancerName = loadBalancerName
				containers = append(containers, container)
			}
		}

		// return containers if found, nil if empty (to trigger retry)
		if len(containers) > 0 {
			return containers, nil
		}
		return nil, nil
	}

	result, err := retryWithTimeout(operation, timeout, retryInterval, fmt.Sprintf("load balancer containers for cluster %s", clusterName))
	if err != nil {
		return nil, err
	}

	if result == nil {
		return []DockerContainer{}, nil
	}

	return result.([]DockerContainer), nil
}

// extractLoadBalancerName extracts the load balancer name from Docker labels
func extractLoadBalancerName(labels string) string {
	// look for pattern: io.x-k8s.cloud-provider-kind.loadbalancer.name=kind1/default/lb-test
	parts := strings.Split(labels, ",")
	for _, part := range parts {
		if strings.Contains(part, "io.x-k8s.cloud-provider-kind.loadbalancer.name=") {
			name := strings.TrimPrefix(part, "io.x-k8s.cloud-provider-kind.loadbalancer.name=")
			// extract just the load balancer name (last part after /)
			nameParts := strings.Split(name, "/")
			if len(nameParts) > 0 {
				return nameParts[len(nameParts)-1]
			}
			return name
		}
	}
	return "unknown"
}

// parsePortMappings parses Docker port mappings
func parsePortMappings(portsStr string) []PortMapping {
	var mappings []PortMapping

	if portsStr == "" {
		return mappings
	}

	// split by comma to get individual port mappings
	portMappings := strings.Split(portsStr, ", ")

	for _, mapping := range portMappings {
		// Example: "0.0.0.0:49778->80/tcp, [::]:49778->80/tcp"
		parts := strings.Split(mapping, "->")
		if len(parts) != 2 {
			continue
		}

		hostPart := strings.TrimSpace(parts[0])
		containerPart := strings.TrimSpace(parts[1])

		// parse host part (e.g., "0.0.0.0:49778" or "[::]:49778")
		var hostPort, ipVersion string
		if strings.HasPrefix(hostPart, "[::]:") {
			ipVersion = "IPv6"
			hostPort = strings.TrimPrefix(hostPart, "[::]:")
		} else if strings.Contains(hostPart, ":") {
			ipVersion = "IPv4"
			hostParts := strings.Split(hostPart, ":")
			if len(hostParts) > 1 {
				hostPort = hostParts[1]
			}
		} else {
			continue
		}

		// parse container part (e.g., "80/tcp")
		containerParts := strings.Split(containerPart, "/")
		if len(containerParts) != 2 {
			continue
		}

		servicePort := containerParts[0]
		protocol := containerParts[1]

		mappings = append(mappings, PortMapping{
			HostPort:    hostPort,
			ServicePort: servicePort,
			Protocol:    protocol,
			IPVersion:   ipVersion,
		})
	}

	return mappings
}
