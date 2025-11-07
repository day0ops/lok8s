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

package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/day0ops/lok8s/pkg/logger"
)

// GetContainerRuntime detects and returns the available container runtime
func GetContainerRuntime() (string, error) {
	// check for Docker
	if err := exec.Command("docker", "version").Run(); err == nil {
		return "docker", nil
	}

	// check for Podman
	if err := exec.Command("podman", "version").Run(); err == nil {
		return "podman", nil
	}

	return "", fmt.Errorf("neither Docker nor Podman is available")
}

// CreateNetwork creates a Docker/Podman network
func CreateNetwork(networkName, gatewayIP, subnetCIDR string) error {
	runtime, err := GetContainerRuntime()
	if err != nil {
		return err
	}

	// check if network already exists
	cmd := exec.Command(runtime, "network", "ls", "--format", "{{.Name}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	networks := strings.Split(string(output), "\n")
	for _, network := range networks {
		if strings.TrimSpace(network) == networkName {
			logger.Infof("network %s already exists", networkName)
			return nil
		}
	}

	// create network
	cmd = exec.Command(runtime, "network", "create", networkName,
		"--gateway="+gatewayIP,
		"--subnet="+subnetCIDR)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create network %s: %w", networkName, err)
	}

	logger.Infof("📡 Network '%s' created with gateway %s and subnet %s", networkName, gatewayIP, subnetCIDR)
	return nil
}

// GetNetworkGateway gets the gateway IP of a Docker network
func GetNetworkGateway(networkName string) (string, error) {
	cmd := exec.Command("docker", "network", "inspect", networkName, "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect network %s: %w", networkName, err)
	}

	// parse JSON output to find IPv4 gateway
	var networkInfo []map[string]interface{}
	if err := json.Unmarshal(output, &networkInfo); err != nil {
		return "", fmt.Errorf("failed to parse network info: %w", err)
	}

	if len(networkInfo) == 0 {
		return "", fmt.Errorf("network %s not found", networkName)
	}

	network := networkInfo[0]
	if ipam, ok := network["IPAM"].(map[string]interface{}); ok {
		if configs, ok := ipam["Config"].([]interface{}); ok {
			for _, config := range configs {
				if configMap, ok := config.(map[string]interface{}); ok {
					if gateway, ok := configMap["Gateway"].(string); ok && gateway != "" {
						return gateway, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("gateway not found for network %s", networkName)
}

// CreateRegistryContainer creates and starts the main registry container
func CreateRegistryContainer(regName, networkName, regPort, registryPort string) error {
	// Check container runtime - only proceed if it's Docker
	containerRuntime, err := GetContainerRuntime()
	if err != nil {
		return fmt.Errorf("failed to get container runtime: %w", err)
	}

	if containerRuntime != "docker" {
		logger.Debugf("skipping registry container setup (container runtime is %s, not docker)", containerRuntime)
		return nil
	}

	// Check if container already exists
	cmd := exec.Command("docker", "ps", "-a", "--filter", fmt.Sprintf("name=%s", regName), "--format", "json")
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		var containers []map[string]interface{}
		// docker ps can return multiple lines (one per container) or a single object
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var containerInfo map[string]interface{}
			if err := json.Unmarshal([]byte(line), &containerInfo); err == nil && len(containerInfo) > 0 {
				containers = append(containers, containerInfo)
			}
		}

		if len(containers) > 0 {
			// Container exists - just skip it, don't try to start or recreate
			containerInfo := containers[0]
			if status, ok := containerInfo["Status"].(string); ok {
				if strings.Contains(status, "Up") || strings.Contains(status, "running") {
					logger.Debugf("registry container %s already exists and is running, skipping", regName)
				} else {
					logger.Debugf("registry container %s already exists (status: %s), skipping", regName, status)
				}
			} else {
				logger.Debugf("registry container %s already exists, skipping", regName)
			}
			return nil
		}
	}

	// create and start new registry container
	cmd = exec.Command("docker", "run", "-d",
		"--name", regName,
		"--network", networkName,
		"--restart", "always",
		"-p", fmt.Sprintf("0.0.0.0:%s:%s", regPort, registryPort),
		"registry:2")

	// capture stderr for better error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorMsg := stderr.String()
		if errorMsg != "" {
			// Check if it's a port conflict
			if strings.Contains(errorMsg, "address already in use") || strings.Contains(errorMsg, "port is already allocated") {
				return fmt.Errorf("port %s is already in use. Please stop the container using this port or use a different port: %s", regPort, strings.TrimSpace(errorMsg))
			}
			return fmt.Errorf("failed to create registry container: %s: %w", strings.TrimSpace(errorMsg), err)
		}
		return fmt.Errorf("failed to create registry container: %w", err)
	}

	logger.Debugf("created registry container %s on port %s", regName, regPort)
	return nil
}

// CreateRegistryMirror creates and starts a registry mirror container
func CreateRegistryMirror(cacheName, cacheURL, networkName, registryPort string) error {
	// Check container runtime - only proceed if it's Docker
	containerRuntime, err := GetContainerRuntime()
	if err != nil {
		return fmt.Errorf("failed to get container runtime: %w", err)
	}

	if containerRuntime != "docker" {
		logger.Debugf("skipping registry mirror setup for %s (container runtime is %s, not docker)", cacheName, containerRuntime)
		return nil
	}

	// Check if container already exists
	cmd := exec.Command("docker", "ps", "-a", "--filter", fmt.Sprintf("name=%s", cacheName), "--format", "json")
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		var containers []map[string]interface{}
		// docker ps can return multiple lines (one per container) or a single object
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var containerInfo map[string]interface{}
			if err := json.Unmarshal([]byte(line), &containerInfo); err == nil && len(containerInfo) > 0 {
				containers = append(containers, containerInfo)
			}
		}

		if len(containers) > 0 {
			// Container exists - just skip it, don't try to start or recreate
			containerInfo := containers[0]
			if status, ok := containerInfo["Status"].(string); ok {
				if strings.Contains(status, "Up") || strings.Contains(status, "running") {
					logger.Debugf("registry mirror %s already exists and is running, skipping", cacheName)
				} else {
					logger.Debugf("registry mirror %s already exists (status: %s), skipping", cacheName, status)
				}
			} else {
				logger.Debugf("registry mirror %s already exists, skipping", cacheName)
			}
			return nil
		}
	}

	// Create registry config
	configContent := fmt.Sprintf(`version: 0.1
proxy:
  remoteurl: %s
log:
  fields:
    service: registry
storage:
  cache:
    blobdescriptor: inmemory
  filesystem:
    rootdirectory: /var/lib/registry
http:
  addr: :%s
  headers:
    X-Content-Type-Options: [nosniff]
health:
  storagedriver:
    enabled: true
    interval: 10s
    threshold: 3
`, cacheURL, registryPort)

	// Write config to temporary file
	tmpDir := os.TempDir()
	configPath := filepath.Join(tmpDir, fmt.Sprintf("docker-config-%s-config.yml", cacheName))

	// check if path exists and is a directory, remove it if so
	if info, err := os.Stat(configPath); err == nil {
		if info.IsDir() {
			if err := os.RemoveAll(configPath); err != nil {
				return fmt.Errorf("failed to remove existing directory at %s: %w", configPath, err)
			}
		} else {
			if err := os.Remove(configPath); err != nil {
				return fmt.Errorf("failed to remove existing file at %s: %w", configPath, err)
			}
		}
	}

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write registry config: %w", err)
	}

	// Create and start new registry mirror container
	cmd = exec.Command("docker", "run", "-d",
		"--name", cacheName,
		"--network", networkName,
		"--restart", "always",
		"-v", fmt.Sprintf("%s:/etc/docker/registry/config.yml", configPath),
		"registry:2")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create registry mirror container %s: %w", cacheName, err)
	}

	logger.Debugf("started registry mirror %s for %s", cacheName, cacheURL)
	return nil
}

// DeleteRegistryContainers deletes registry containers
func DeleteRegistryContainers(containerNames []string) error {
	// Check container runtime - only proceed if it's Docker
	containerRuntime, err := GetContainerRuntime()
	if err != nil {
		return fmt.Errorf("failed to get container runtime: %w", err)
	}

	if containerRuntime != "docker" {
		logger.Debugf("skipping registry container deletion (container runtime is %s, not docker)", containerRuntime)
		return nil
	}

	for _, containerName := range containerNames {
		// Check if container exists - docker filter name= matches substrings, so we need to check exact match
		cmd := exec.Command("docker", "ps", "-a", "--filter", fmt.Sprintf("name=%s", containerName), "--format", "{{.Names}}")
		output, err := cmd.Output()
		if err != nil {
			logger.Debugf("failed to check for container %s: %v", containerName, err)
			continue
		}

		if len(output) > 0 {
			// Parse all lines and check for exact match (docker filter can match substrings)
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			found := false
			for _, line := range lines {
				name := strings.TrimSpace(line)
				if name == containerName {
					found = true
					// Container exists with exact name match, delete it
					cmd = exec.Command("docker", "rm", "-f", containerName)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil {
						errorMsg := stderr.String()
						if errorMsg != "" {
							logger.Warnf("failed to delete registry container %s: %s", containerName, strings.TrimSpace(errorMsg))
						} else {
							logger.Warnf("failed to delete registry container %s: %v", containerName, err)
						}
					} else {
						logger.Infof("deleted registry container %s", containerName)
					}
					break // Found and deleted, move to next container
				}
			}
			if !found {
				logger.Debugf("container %s not found (filter matched but name didn't match exactly)", containerName)
			}
		} else {
			logger.Debugf("container %s doesn't exist", containerName)
		}
	}

	return nil
}
