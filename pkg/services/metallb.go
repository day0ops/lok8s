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
	"net"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util/helm"
	"github.com/day0ops/lok8s/pkg/util/k8s"
)

// MetalLBManager manages MetalLB installation and configuration
type MetalLBManager struct {
	helmManager   *helm.HelmManager
	minOctetRange int
	maxOctetRange int
	configManager *config.ConfigManager
	ipAllocations map[string]*config.MetalLBAllocation // in-memory tracking during cluster creation
	usedRanges    map[string]bool                      // tracks used IP ranges (ipPrefix.start-end)
	allNodeIPs    map[int]bool                         // tracks all node IPs across clusters
}

// NewMetalLBManager creates a new MetalLB manager
func NewMetalLBManager(helmManager *helm.HelmManager) *MetalLBManager {
	return &MetalLBManager{
		helmManager:   helmManager,
		configManager: config.NewConfigManager(),
		ipAllocations: make(map[string]*config.MetalLBAllocation),
		usedRanges:    make(map[string]bool),
		allNodeIPs:    make(map[int]bool),
	}
}

func NewMetalLBManagerWithOptions(helmManager *helm.HelmManager, minOctetRange, maxOctetRange int) *MetalLBManager {
	return &MetalLBManager{
		helmManager:   helmManager,
		minOctetRange: minOctetRange,
		maxOctetRange: maxOctetRange,
		configManager: config.NewConfigManager(),
		ipAllocations: make(map[string]*config.MetalLBAllocation),
		usedRanges:    make(map[string]bool),
		allNodeIPs:    make(map[int]bool),
	}
}

// InitializeTracking initializes IP tracking from saved config or starts fresh
func (mm *MetalLBManager) InitializeTracking(project string) error {
	projectConfig, err := mm.configManager.LoadConfig(project)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// clear existing tracking
	mm.ipAllocations = make(map[string]*config.MetalLBAllocation)
	mm.usedRanges = make(map[string]bool)
	mm.allNodeIPs = make(map[int]bool)

	// load existing allocations from config
	if projectConfig != nil && len(projectConfig.MetalLBAllocations) > 0 {
		for _, alloc := range projectConfig.MetalLBAllocations {
			mm.ipAllocations[alloc.ClusterName] = &alloc
			// track used ranges
			rangeKey := fmt.Sprintf("%s.%d-%d", alloc.IPPrefix, alloc.StartOctet, alloc.EndOctet)
			mm.usedRanges[rangeKey] = true
			// track node IPs
			for _, nodeIP := range alloc.NodeIPs {
				mm.allNodeIPs[nodeIP] = true
			}
			logger.Debugf("loaded existing MetalLB allocation for cluster %s: %s", alloc.ClusterName, alloc.IPRange)
		}
	}

	return nil
}

// SaveAllocation saves the IP allocation for a cluster to the project config
func (mm *MetalLBManager) SaveAllocation(project string, allocation *config.MetalLBAllocation) error {
	// load existing config
	projectConfig, err := mm.configManager.LoadConfig(project)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// create new config if it doesn't exist
	if projectConfig == nil {
		projectConfig = &config.ProjectConfig{
			Project: project,
		}
	}

	// add or update allocation
	found := false
	for i, existing := range projectConfig.MetalLBAllocations {
		if existing.ClusterName == allocation.ClusterName {
			projectConfig.MetalLBAllocations[i] = *allocation
			found = true
			break
		}
	}
	if !found {
		projectConfig.MetalLBAllocations = append(projectConfig.MetalLBAllocations, *allocation)
	}

	// save config
	if err := mm.configManager.SaveConfig(project, projectConfig); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}

	// update in-memory tracking
	mm.ipAllocations[allocation.ClusterName] = allocation
	rangeKey := fmt.Sprintf("%s.%d-%d", allocation.IPPrefix, allocation.StartOctet, allocation.EndOctet)
	mm.usedRanges[rangeKey] = true
	for _, nodeIP := range allocation.NodeIPs {
		mm.allNodeIPs[nodeIP] = true
	}

	logger.Debugf("saved MetalLB allocation for cluster %s: %s", allocation.ClusterName, allocation.IPRange)
	return nil
}

// InstallMetalLB installs MetalLB using Helm
func (mm *MetalLBManager) InstallMetalLB(clusterName string) error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("installing MetalLB on cluster %s", clusterName))
	defer func() {
		if status != nil {
			status.End(true)
		}
	}()

	// add metallb repository
	if err := mm.helmManager.AddRepository("metallb", "https://metallb.github.io/metallb"); err != nil {
		status.End(false)
		return fmt.Errorf("failed to add metallb repository: %w", err)
	}

	// install metallb chart
	values := map[string]interface{}{
		"controller": map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "100m",
					"memory": "100Mi",
				},
			},
		},
		"speaker": map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "100m",
					"memory": "100Mi",
				},
			},
			"affinity": map[string]interface{}{
				"nodeAffinity": map[string]interface{}{
					"requiredDuringSchedulingIgnoredDuringExecution": map[string]interface{}{
						"nodeSelectorTerms": []map[string]interface{}{
							{
								"matchExpressions": []map[string]interface{}{
									{
										"key":      "node.kubernetes.io/exclude-from-external-load-balancers",
										"operator": "DoesNotExist",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := mm.helmManager.InstallChart("metallb", "metallb/metallb", "metallb-system", values, 5*time.Minute); err != nil {
		status.End(false)
		return fmt.Errorf("failed to install metallb chart: %w", err)
	}

	// wait for metallb pods to be ready
	if err := mm.WaitForMetalLBReady(clusterName); err != nil {
		status.End(false)
		return fmt.Errorf("metallb pods not ready: %w", err)
	}

	// Success - status.End(true) will be called by defer
	return nil
}

// ConfigureMetalLB configures MetalLB with IP address pool
func (mm *MetalLBManager) ConfigureMetalLB(clusterName, minikubeIp string, clusterNumber int, totalClusters int, project string) error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("configuring MetalLB on cluster %s", clusterName))
	defer func() {
		if status != nil {
			status.End(true)
		}
	}()

	// create client manager for the cluster
	clientManager, err := k8s.NewClientManagerForContext(clusterName)
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed to create kubernetes client manager: %w", err)
	}

	// generate dynamic IP range based on cluster network and number
	ipRange, allocation, err := mm.generateMetalLBIPRange(clusterName, minikubeIp, clusterNumber, totalClusters, clientManager)
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed to generate MetalLB IP range: %w", err)
	}

	logger.Debugf("using MetalLB IP range: %s", ipRange)

	// create IP address pool
	ipPool := fmt.Sprintf(`
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: default-pool
  namespace: metallb-system
spec:
  addresses:
  - %s
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: default-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - default-pool
`, ipRange)

	// apply the configuration using client manager
	if err := clientManager.ApplyManifest(ipPool); err != nil {
		status.End(false)
		return fmt.Errorf("failed to apply metallb configuration: %w", err)
	}

	// save allocation to config
	if project != "" {
		if err := mm.SaveAllocation(project, allocation); err != nil {
			logger.Warnf("failed to save MetalLB allocation to config: %v", err)
		}
	}

	// Success - status.End(true) will be called by defer
	return nil
}

// WaitForMetalLBReady waits for MetalLB to be ready
func (mm *MetalLBManager) WaitForMetalLBReady(clusterName string) error {
	client, err := mm.helmManager.GetKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %w", err)
	}

	ctx := context.Background()
	timeout := 5 * time.Minute
	deadline := time.Now().Add(timeout)

	logger.Debugf("waiting for MetalLB controller and speaker pods to be ready...")

	for time.Now().Before(deadline) {
		// check metallb controller deployment
		deployments, err := client.AppsV1().Deployments("metallb-system").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=metallb,app.kubernetes.io/component=controller",
		})
		if err != nil {
			logger.Debugf("failed to list metallb controller deployments: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// check metallb speaker daemonset
		daemonsets, err := client.AppsV1().DaemonSets("metallb-system").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=metallb,app.kubernetes.io/component=speaker",
		})
		if err != nil {
			logger.Debugf("failed to list metallb speaker daemonsets: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// check controller readiness
		controllerReady := false
		if len(deployments.Items) > 0 {
			deployment := deployments.Items[0]
			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
				controllerReady = true
			}
		}

		// check speaker readiness
		speakerReady := false
		if len(daemonsets.Items) > 0 {
			ds := daemonsets.Items[0]
			if ds.Status.DesiredNumberScheduled > 0 && ds.Status.NumberReady == ds.Status.DesiredNumberScheduled {
				speakerReady = true
			}
		}

		logger.Debugf("MetalLB status - controller: %v, speaker: %v",
			controllerReady, speakerReady)

		// all components ready
		if controllerReady && speakerReady {
			logger.Debugf("MetalLB is ready on cluster %s", clusterName)
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for MetalLB to be ready on cluster %s", clusterName)
}

// generateMetalLBIPRange generates a dynamic IP range for MetalLB based on cluster network and number
// Uses the first 3 octets from minikubeIP and splits the last octet range between clusters
// Allocates 20 IPs per cluster and avoids overlap with node IPs and previously used ranges
func (mm *MetalLBManager) generateMetalLBIPRange(clusterName, minikubeIP string, clusterNumber, totalClusters int, clientManager *k8s.ClientManager) (string, *config.MetalLBAllocation, error) {
	// extract first 3 octets from minikubeIP (x.x.x)
	ipParts := strings.Split(minikubeIP, ".")
	if len(ipParts) < 3 {
		return "", nil, fmt.Errorf("invalid minikube IP format: %s", minikubeIP)
	}
	ipPrefix := fmt.Sprintf("%s.%s.%s", ipParts[0], ipParts[1], ipParts[2])

	// get node IPs from current cluster
	currentNodeIPs, err := mm.getNodeIPs(clientManager)
	if err != nil {
		logger.Warnf("failed to get node IPs, continuing without overlap check: %v", err)
		currentNodeIPs = make(map[int]bool)
	}

	// merge with all previously tracked node IPs
	combinedNodeIPs := make(map[int]bool)
	for octet := range mm.allNodeIPs {
		combinedNodeIPs[octet] = true
	}
	for octet := range currentNodeIPs {
		combinedNodeIPs[octet] = true
	}

	// calculate available IP range
	// use minOctetRange to maxOctetRange (e.g., 200-254 = 55 IPs)
	totalAvailableIPs := mm.maxOctetRange - mm.minOctetRange + 1
	ipsPerCluster := 20

	// calculate how many clusters we can fit
	maxClusters := totalAvailableIPs / ipsPerCluster
	if totalClusters > maxClusters {
		return "", nil, fmt.Errorf("not enough IPs available: need %d clusters but only %d can fit in range %d-%d (20 IPs per cluster)", totalClusters, maxClusters, mm.minOctetRange, mm.maxOctetRange)
	}

	// calculate start octet for this cluster
	startOctet := mm.minOctetRange + (clusterNumber-1)*ipsPerCluster
	endOctet := startOctet + ipsPerCluster - 1

	// ensure we don't exceed maxOctetRange
	if endOctet > mm.maxOctetRange {
		endOctet = mm.maxOctetRange
	}

	// check if this range is already used by another cluster
	rangeKey := fmt.Sprintf("%s.%d-%d", ipPrefix, startOctet, endOctet)
	if mm.usedRanges[rangeKey] {
		// find next available range
		startOctet, endOctet = mm.findNextAvailableRange(startOctet, endOctet, ipsPerCluster, combinedNodeIPs, ipPrefix)
	}

	// filter out node IPs from the range
	startOctet, endOctet = mm.adjustRangeForNodeIPs(startOctet, endOctet, combinedNodeIPs, ipPrefix)

	// build IP range string (recalculate rangeKey after adjustments)
	ipRange := fmt.Sprintf("%s.%d-%s.%d", ipPrefix, startOctet, ipPrefix, endOctet)

	// convert node IPs map to slice for storage
	nodeIPsSlice := make([]int, 0, len(currentNodeIPs))
	for octet := range currentNodeIPs {
		nodeIPsSlice = append(nodeIPsSlice, octet)
	}

	// create allocation record
	allocation := &config.MetalLBAllocation{
		ClusterName: clusterName,
		IPPrefix:    ipPrefix,
		StartOctet:  startOctet,
		EndOctet:    endOctet,
		NodeIPs:     nodeIPsSlice,
		IPRange:     ipRange,
	}

	logger.Debugf("generated MetalLB IP range for cluster %s (number %d/%d): %s (avoided %d node IPs, %d previously used ranges)", clusterName, clusterNumber, totalClusters, ipRange, len(combinedNodeIPs), len(mm.usedRanges))
	return ipRange, allocation, nil
}

// findNextAvailableRange finds the next available IP range that doesn't conflict with used ranges
func (mm *MetalLBManager) findNextAvailableRange(startOctet, endOctet, rangeSize int, nodeIPs map[int]bool, ipPrefix string) (int, int) {
	attempts := 0
	maxAttempts := 100

	for attempts < maxAttempts {
		// check if this range conflicts with any used ranges for the same IP prefix
		rangeKey := fmt.Sprintf("%s.%d-%d", ipPrefix, startOctet, endOctet)
		if mm.usedRanges[rangeKey] {
			// range already used, try next
			startOctet++
			endOctet = startOctet + rangeSize - 1
			if endOctet > mm.maxOctetRange {
				startOctet = mm.minOctetRange
				endOctet = startOctet + rangeSize - 1
			}
			attempts++
			continue
		}

		// check if range overlaps with node IPs
		hasOverlap := false
		for octet := startOctet; octet <= endOctet; octet++ {
			if nodeIPs[octet] {
				hasOverlap = true
				break
			}
		}
		if !hasOverlap {
			return startOctet, endOctet
		}

		// move to next range
		startOctet++
		endOctet = startOctet + rangeSize - 1

		// wrap around if we exceed max
		if endOctet > mm.maxOctetRange {
			startOctet = mm.minOctetRange
			endOctet = startOctet + rangeSize - 1
		}

		attempts++
	}

	// fallback to original range if we can't find a free one
	logger.Warnf("could not find completely free range after %d attempts, using original range", attempts)
	return startOctet, endOctet
}

// getNodeIPs retrieves all node IP addresses from the cluster
func (mm *MetalLBManager) getNodeIPs(clientManager *k8s.ClientManager) (map[int]bool, error) {
	nodeIPs := make(map[int]bool)

	client := clientManager.GetClientset()
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" || addr.Type == "ExternalIP" {
				ip := net.ParseIP(addr.Address)
				if ip != nil && ip.To4() != nil {
					// extract last octet
					ipParts := strings.Split(addr.Address, ".")
					if len(ipParts) == 4 {
						if lastOctet, err := strconv.Atoi(ipParts[3]); err == nil {
							nodeIPs[lastOctet] = true
							logger.Debugf("found node IP: %s (last octet: %d)", addr.Address, lastOctet)
						}
					}
				}
			}
		}
	}

	return nodeIPs, nil
}

// adjustRangeForNodeIPs adjusts the IP range to avoid node IPs
// if node IPs are found in the range, it shifts the range up
func (mm *MetalLBManager) adjustRangeForNodeIPs(startOctet, endOctet int, nodeIPs map[int]bool, ipPrefix string) (int, int) {
	// check if any node IPs are in our range
	hasOverlap := false
	for octet := startOctet; octet <= endOctet; octet++ {
		if nodeIPs[octet] {
			hasOverlap = true
			logger.Debugf("node IP found at %s.%d, adjusting range", ipPrefix, octet)
			break
		}
	}

	// if overlap found, try to shift range up
	if hasOverlap {
		newStart := startOctet
		newEnd := endOctet
		rangeSize := endOctet - startOctet + 1

		// try to find a contiguous range without node IPs
		for attempt := 0; attempt < 10; attempt++ {
			// check if this range is free
			free := true
			for octet := newStart; octet <= newEnd; octet++ {
				if nodeIPs[octet] || octet > mm.maxOctetRange {
					free = false
					break
				}
			}

			if free {
				return newStart, newEnd
			}

			// shift up by 1
			newStart++
			newEnd = newStart + rangeSize - 1

			// if we exceed max, wrap around from min
			if newEnd > mm.maxOctetRange {
				newStart = mm.minOctetRange
				newEnd = newStart + rangeSize - 1
			}
		}

		// if we couldn't find a free range, log warning and use original
		logger.Warnf("could not find completely free range, using original range with potential overlap")
	}

	return startOctet, endOctet
}
