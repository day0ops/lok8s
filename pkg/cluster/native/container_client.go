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
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util"
)

// containerBinary is the Apple `container` CLI (github.com/apple/container).
const containerBinary = "container"

// ContainerClient wraps the `container` CLI for driving Apple's native
// Containerization VMs. It is a thin exec wrapper, not a client library,
// mirroring the pattern already used for docker/podman in pkg/util/docker.
type ContainerClient struct{}

// NewContainerClient creates a new container CLI client
func NewContainerClient() *ContainerClient {
	return &ContainerClient{}
}

// RunOptions describes a single node VM to boot via `container run`
type RunOptions struct {
	Name    string
	Network string
	CPUs    string
	Memory  string
	Image   string
	Args    []string
}

// containerInspectNetwork mirrors the relevant fields of `container inspect`'s
// status.networks[] entries
type containerInspectNetwork struct {
	Hostname    string `json:"hostname"`
	IPv4Address string `json:"ipv4Address"`
	Network     string `json:"network"`
}

// containerInspectStatus mirrors the relevant fields of `container inspect`'s status object
type containerInspectStatus struct {
	State    string                    `json:"state"`
	Networks []containerInspectNetwork `json:"networks"`
}

// containerInspectEntry mirrors a single entry of `container inspect`'s JSON array output
type containerInspectEntry struct {
	ID     string                 `json:"id"`
	Status containerInspectStatus `json:"status"`
}

// networkConfiguration mirrors the relevant fields of `container network list`'s
// configuration object
type networkConfiguration struct {
	Name string `json:"name"`
}

// networkStatus mirrors the relevant fields of `container network list`'s status object
type networkStatus struct {
	IPv4Subnet string `json:"ipv4Subnet"`
}

// networkEntry mirrors a single entry of `container network list`'s JSON array output
type networkEntry struct {
	ID            string               `json:"id"`
	Configuration networkConfiguration `json:"configuration"`
	Status        networkStatus        `json:"status"`
}

// Version returns the installed `container` CLI version (e.g. "1.3.0")
func (c *ContainerClient) Version() (string, error) {
	cmd := exec.Command(containerBinary, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run '%s --version': %w", containerBinary, err)
	}

	// output looks like: "container CLI version 1.3.0 (build: release, commit: unspecified)"
	fields := strings.Fields(string(output))
	for i, field := range fields {
		if field == "version" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("unable to parse container CLI version from: %s", strings.TrimSpace(string(output)))
}

// SystemRunning reports whether the `container` system services (apiserver, etc.) are running
func (c *ContainerClient) SystemRunning() bool {
	cmd := exec.Command(containerBinary, "system", "status", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return false
	}

	return status.Status == "running"
}

// listNetworks lists all `container` networks
func (c *ContainerClient) listNetworks() ([]networkEntry, error) {
	cmd := exec.Command(containerBinary, "network", "list", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list container networks: %w", err)
	}

	var entries []networkEntry
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse container network list: %w", err)
	}

	return entries, nil
}

// ListNetworkSubnets returns a map of network name to its IPv4 subnet CIDR
func (c *ContainerClient) ListNetworkSubnets() (map[string]string, error) {
	entries, err := c.listNetworks()
	if err != nil {
		return nil, err
	}

	subnets := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Status.IPv4Subnet != "" {
			subnets[entry.Configuration.Name] = entry.Status.IPv4Subnet
		}
	}

	return subnets, nil
}

// NetworkExists reports whether a network with the given name already exists
func (c *ContainerClient) NetworkExists(name string) (bool, error) {
	entries, err := c.listNetworks()
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.Configuration.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// CreateNetwork creates a `container` network with the given subnet CIDR.
// It is a no-op if the network already exists.
func (c *ContainerClient) CreateNetwork(name, subnetCIDR string) error {
	exists, err := c.NetworkExists(name)
	if err != nil {
		return fmt.Errorf("failed to check for existing network %s: %w", name, err)
	}
	if exists {
		logger.Debugf("network %s already exists", name)
		return nil
	}

	cmd := exec.Command(containerBinary, "network", "create", name, "--subnet", subnetCIDR)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to create network %s: %w: %s", name, err, output)
		}
		return fmt.Errorf("failed to create network %s: %w", name, err)
	}

	logger.Debugf("created network %s with subnet %s", name, subnetCIDR)
	return nil
}

// DeleteNetwork deletes a `container` network. Deleting a network that
// doesn't exist is treated as success, matching the idempotent delete
// semantics used elsewhere in lok8s (e.g. k8s.DeleteContext).
func (c *ContainerClient) DeleteNetwork(name string) error {
	cmd := exec.Command(containerBinary, "network", "delete", name)
	output, err := util.RunCommand(cmd)
	if err != nil {
		if strings.Contains(output, "not found") {
			logger.Debugf("network %s already deleted", name)
			return nil
		}
		if output != "" {
			return fmt.Errorf("failed to delete network %s: %w: %s", name, err, output)
		}
		return fmt.Errorf("failed to delete network %s: %w", name, err)
	}

	return nil
}

// Run boots a node VM via `container run -d`. Nodes get the full guest
// kernel capability set (--cap-add ALL) since the VM boundary, not Linux
// capabilities, is what isolates one node from another.
func (c *ContainerClient) Run(opts RunOptions) error {
	args := []string{
		"run", "-d",
		"--name", opts.Name,
		"--network", opts.Network,
		"--cap-add", "ALL",
		"--masked-path", "NONE",
		"--read-only-path", "NONE",
	}
	if opts.CPUs != "" {
		args = append(args, "--cpus", opts.CPUs)
	}
	if opts.Memory != "" {
		args = append(args, "--memory", opts.Memory)
	}
	args = append(args, opts.Image)
	args = append(args, opts.Args...)

	cmd := exec.Command(containerBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to run node %s: %w: %s", opts.Name, err, output)
		}
		return fmt.Errorf("failed to run node %s: %w", opts.Name, err)
	}

	return nil
}

// Exec runs a command inside a running node VM and returns its stdout
func (c *ContainerClient) Exec(nodeName string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", nodeName}, args...)
	cmd := exec.Command(containerBinary, cmdArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}

	return stdout.String(), nil
}

// inspect returns the current state of a node VM
func (c *ContainerClient) inspect(nodeName string) (*containerInspectEntry, error) {
	cmd := exec.Command(containerBinary, "inspect", nodeName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect node %s: %w", nodeName, err)
	}

	var entries []containerInspectEntry
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse inspect output for node %s: %w", nodeName, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no inspect data returned for node %s", nodeName)
	}

	return &entries[0], nil
}

// State returns the current run state of a node VM (e.g. "running")
func (c *ContainerClient) State(nodeName string) (string, error) {
	entry, err := c.inspect(nodeName)
	if err != nil {
		return "", err
	}

	return entry.Status.State, nil
}

// IPv4Address returns the node's routable IPv4 address on the given network,
// with any CIDR suffix (e.g. "/24") stripped
func (c *ContainerClient) IPv4Address(nodeName, network string) (string, error) {
	entry, err := c.inspect(nodeName)
	if err != nil {
		return "", err
	}

	for _, net := range entry.Status.Networks {
		if net.Network == network && net.IPv4Address != "" {
			return strings.SplitN(net.IPv4Address, "/", 2)[0], nil
		}
	}

	return "", fmt.Errorf("no IPv4 address found for node %s on network %s", nodeName, network)
}

// ListNodeNames lists the names of all node VMs (running or stopped) whose
// name starts with the given prefix
func (c *ContainerClient) ListNodeNames(prefix string) ([]string, error) {
	cmd := exec.Command(containerBinary, "list", "-a", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var entries []containerInspectEntry
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse container list: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.ID, prefix) {
			names = append(names, entry.ID)
		}
	}

	return names, nil
}

// Delete deletes one or more node VMs, forcing removal even if still running
func (c *ContainerClient) Delete(nodeNames ...string) error {
	if len(nodeNames) == 0 {
		return nil
	}

	args := append([]string{"delete", "-f"}, nodeNames...)
	cmd := exec.Command(containerBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to delete nodes %s: %w: %s", strings.Join(nodeNames, ", "), err, output)
		}
		return fmt.Errorf("failed to delete nodes %s: %w", strings.Join(nodeNames, ", "), err)
	}

	return nil
}
