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

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/day0ops/lok8s/pkg/logger"
	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the configuration for a specific project
type ProjectConfig struct {
	Project     string `yaml:"project"`
	Environment string `yaml:"environment"`

	// common options
	NumClusters int    `yaml:"num_clusters"`
	NodeCount   int    `yaml:"node_count"`
	K8sVersion  string `yaml:"k8s_version"`

	// network options
	GatewayIP  string `yaml:"gateway_ip"`
	SubnetCIDR string `yaml:"subnet_cidr"`
	Bridge     string `yaml:"bridge"`

	// minikube specific options
	CPU      string `yaml:"cpu"`
	Memory   string `yaml:"memory"`
	DiskSize string `yaml:"disk_size"`

	// kind specific options
	CNI              string `yaml:"cni"`
	ContainerRuntime string `yaml:"container_runtime"`
	ContainerEngine  string `yaml:"container_engine"`

	// load balancer options
	InstallMetalLB       bool `yaml:"install_metallb"`
	InstallCloudProvider bool `yaml:"install_cloud_provider"`
	SkipMetalLB          bool `yaml:"skip_metallb"`

	// MetalLB IP allocation tracking
	MetalLBAllocations []MetalLBAllocation `yaml:"metallb_allocations,omitempty"`
}

// MetalLBAllocation tracks IP ranges and node IPs for a cluster
type MetalLBAllocation struct {
	ClusterName string `yaml:"cluster_name"`
	IPPrefix    string `yaml:"ip_prefix"`   // first 3 octets (x.x.x)
	StartOctet  int    `yaml:"start_octet"` // start of IP range
	EndOctet    int    `yaml:"end_octet"`   // end of IP range
	NodeIPs     []int  `yaml:"node_ips"`    // node IP last octets
	IPRange     string `yaml:"ip_range"`    // full IP range string (x.x.x.start-x.x.x.end)
}

// ConfigManager handles project configuration persistence
type ConfigManager struct {
	configDir string
}

// NewConfigManager creates a new config manager
func NewConfigManager() *ConfigManager {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf("failed to get home directory: %v", err)
		homeDir = "."
	}

	configDir := filepath.Join(homeDir, "."+AppName)

	return &ConfigManager{
		configDir: configDir,
	}
}

// NewConfigManagerWithDir creates a new config manager with a custom config directory
// This is useful for testing or when you want to use a specific directory
func NewConfigManagerWithDir(configDir string) *ConfigManager {
	return &ConfigManager{
		configDir: configDir,
	}
}

// GetConfigPath returns the path for a project's config file
func (cm *ConfigManager) GetConfigPath(project string) string {
	return filepath.Join(cm.configDir, project+".yaml")
}

// LoadConfig loads configuration for a project
func (cm *ConfigManager) LoadConfig(project string) (*ProjectConfig, error) {
	configPath := cm.GetConfigPath(project)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logger.Debugf("no config file found for project %s at %s", project, configPath)
		return nil, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var config ProjectConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	logger.Debugf("loaded config for project %s from %s", project, configPath)
	return &config, nil
}

// SaveConfig saves configuration for a project
func (cm *ConfigManager) SaveConfig(project string, config *ProjectConfig) error {
	// ensure config directory exists
	if err := os.MkdirAll(cm.configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", cm.configDir, err)
	}

	configPath := cm.GetConfigPath(project)

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", configPath, err)
	}

	logger.Debugf("saved config for project %s to %s", project, configPath)
	return nil
}

// DeleteConfig deletes configuration for a project
func (cm *ConfigManager) DeleteConfig(project string) error {
	configPath := cm.GetConfigPath(project)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logger.Debugf("config file for project %s does not exist", project)
		return nil
	}

	if err := os.Remove(configPath); err != nil {
		return fmt.Errorf("failed to delete config file %s: %w", configPath, err)
	}

	logger.Debugf("deleted config for project %s at %s", project, configPath)
	return nil
}

// ListConfigs lists all available project configs
func (cm *ConfigManager) ListConfigs() ([]string, error) {
	// ensure config directory exists
	if err := os.MkdirAll(cm.configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory %s: %w", cm.configDir, err)
	}

	entries, err := os.ReadDir(cm.configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read config directory %s: %w", cm.configDir, err)
	}

	var projects []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
			project := entry.Name()[:len(entry.Name())-5] // remove .yaml extension
			projects = append(projects, project)
		}
	}

	return projects, nil
}

// LoadConfigFromFile loads configuration from a user-defined YAML file
func LoadConfigFromFile(filePath string) (*ProjectConfig, error) {
	// check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file does not exist: %s", filePath)
	}

	// read config file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	var config ProjectConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", filePath, err)
	}

	logger.Debugf("loaded config from file: %s", filePath)
	return &config, nil
}

// MergeConfigs merges two configurations, with the second one taking precedence
func MergeConfigs(base, override *ProjectConfig) *ProjectConfig {
	merged := *base

	// override with non-zero/non-empty values from override config
	if override.Project != "" {
		merged.Project = override.Project
	}
	if override.Environment != "" {
		merged.Environment = override.Environment
	}
	if override.NumClusters > 0 {
		merged.NumClusters = override.NumClusters
	}
	if override.NodeCount > 0 {
		merged.NodeCount = override.NodeCount
	}
	if override.K8sVersion != "" {
		merged.K8sVersion = override.K8sVersion
	}
	if override.GatewayIP != "" {
		merged.GatewayIP = override.GatewayIP
	}
	if override.SubnetCIDR != "" {
		merged.SubnetCIDR = override.SubnetCIDR
	}
	if override.Bridge != "" {
		merged.Bridge = override.Bridge
	}
	if override.CPU != "" {
		merged.CPU = override.CPU
	}
	if override.Memory != "" {
		merged.Memory = override.Memory
	}
	if override.DiskSize != "" {
		merged.DiskSize = override.DiskSize
	}
	if override.CNI != "" {
		merged.CNI = override.CNI
	}
	if override.ContainerRuntime != "" {
		merged.ContainerRuntime = override.ContainerRuntime
	}
	if override.ContainerEngine != "" {
		merged.ContainerEngine = override.ContainerEngine
	}

	// boolean flags are always overridden
	merged.InstallMetalLB = override.InstallMetalLB
	merged.InstallCloudProvider = override.InstallCloudProvider
	merged.SkipMetalLB = override.SkipMetalLB

	return &merged
}

// MergeConfig merges command line options with saved config
func (cm *ConfigManager) MergeConfig(project string, cmdConfig *ProjectConfig) (*ProjectConfig, error) {
	// load saved config
	savedConfig, err := cm.LoadConfig(project)
	if err != nil {
		return nil, err
	}

	// if no saved config, use command line config as-is
	if savedConfig == nil {
		return cmdConfig, nil
	}

	// merge saved config with command line config
	mergedConfig := *savedConfig

	// override with non-zero/non-empty values from command line
	if cmdConfig.Project != "" {
		mergedConfig.Project = cmdConfig.Project
	}
	if cmdConfig.Environment != "" {
		mergedConfig.Environment = cmdConfig.Environment
	}
	if cmdConfig.NumClusters > 0 {
		mergedConfig.NumClusters = cmdConfig.NumClusters
	}
	if cmdConfig.NodeCount > 0 {
		mergedConfig.NodeCount = cmdConfig.NodeCount
	}
	if cmdConfig.K8sVersion != "" {
		mergedConfig.K8sVersion = cmdConfig.K8sVersion
	}
	if cmdConfig.GatewayIP != "" {
		mergedConfig.GatewayIP = cmdConfig.GatewayIP
	}
	if cmdConfig.SubnetCIDR != "" {
		mergedConfig.SubnetCIDR = cmdConfig.SubnetCIDR
	}
	if cmdConfig.Bridge != "" {
		mergedConfig.Bridge = cmdConfig.Bridge
	}
	if cmdConfig.CPU != "" {
		mergedConfig.CPU = cmdConfig.CPU
	}
	if cmdConfig.Memory != "" {
		mergedConfig.Memory = cmdConfig.Memory
	}
	if cmdConfig.DiskSize != "" {
		mergedConfig.DiskSize = cmdConfig.DiskSize
	}
	if cmdConfig.CNI != "" {
		mergedConfig.CNI = cmdConfig.CNI
	}
	if cmdConfig.ContainerRuntime != "" {
		mergedConfig.ContainerRuntime = cmdConfig.ContainerRuntime
	}
	if cmdConfig.ContainerEngine != "" {
		mergedConfig.ContainerEngine = cmdConfig.ContainerEngine
	}

	// boolean flags are always overridden by command line
	mergedConfig.InstallMetalLB = cmdConfig.InstallMetalLB
	mergedConfig.InstallCloudProvider = cmdConfig.InstallCloudProvider
	mergedConfig.SkipMetalLB = cmdConfig.SkipMetalLB

	return &mergedConfig, nil
}
