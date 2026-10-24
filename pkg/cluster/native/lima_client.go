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

// LimaClient is the cross-platform NodeClient implementation, driving Lima
// (https://lima-vm.io/) VMs via its `limactl` CLI. Unlike container_client.go
// and incus_client.go, which are each gated to one OS by a //go:build tag,
// this file compiles on every platform: limactl itself behaves identically
// on macOS and Linux, including its "shared" network mode (backed by
// socket_vmnet on macOS, Lima's own bundled privileged-network helper on
// Linux) and the `limactl sudoers` setup both need.
//
// Like Incus, a Lima VM boots a general-purpose OS image rather than an OCI
// image acting as the workload directly, so BootControlPlane/BootWorker do
// the same two-step dance Incus's do: launch the VM, wait for it to become
// reachable, then install and start k3s inside it via its own install
// script (the same installK3sScript constant incus_client.go already
// defines).
//
// wrapOutputError is NOT redefined here: it's already defined once, in
// whichever of container_client.go (darwin) or incus_client.go (linux) is
// active for the current build, and this file compiles alongside whichever
// one that is.

package native

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util"
	"github.com/day0ops/lok8s/pkg/util/version"
	"gopkg.in/yaml.v3"
)

// limactlBinary is the Lima CLI (https://lima-vm.io/).
const limactlBinary = "limactl"

// LimaClient wraps the `limactl` CLI. It is a thin exec wrapper, not a
// client library, mirroring the pattern used for Apple's `container` CLI
// and Incus.
type LimaClient struct{}

var _ NodeClient = (*LimaClient)(nil)

// newLimaClient creates the Lima NodeClient implementation.
func newLimaClient() (NodeClient, error) {
	return &LimaClient{}, nil
}

// CheckPrerequisites verifies the `limactl` CLI is installed and at a
// supported version. Unlike container/incus, limactl is never auto-
// installed - it ships as a full bin/libexec/share package tree meant to be
// installed like Homebrew, not a single binary, so auto-installing it risks
// colliding with a Lima a user already has.
func (c *LimaClient) CheckPrerequisites() error {
	installedVersion, err := c.Version()
	if err != nil {
		return fmt.Errorf("limactl not found or not working: %w (install it from https://lima-vm.io/docs/installation/, e.g. brew install lima)", err)
	}

	if version.Compare(config.NativeLimaMinSupportedVersion, installedVersion) > 0 {
		return fmt.Errorf("limactl version %s is too old. Minimum supported version is %s", installedVersion, config.NativeLimaMinSupportedVersion)
	}
	logger.Debugf("using limactl version: %s", installedVersion)

	return nil
}

// Version returns the installed `limactl` CLI's version (e.g. "2.2.1").
func (c *LimaClient) Version() (string, error) {
	cmd := exec.Command(limactlBinary, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run '%s --version': %w", limactlBinary, wrapOutputError(err))
	}

	return parseLimactlVersion(output)
}

// parseLimactlVersion extracts the version from `limactl --version`'s
// cobra-default output, e.g. "limactl version 2.2.1".
func parseLimactlVersion(output []byte) (string, error) {
	fields := strings.Fields(string(output))
	for i, field := range fields {
		if field == "version" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("unable to parse limactl version from: %s", strings.TrimSpace(string(output)))
}

// limaImageSpec mirrors the fields of a Lima template's `images[]` entry
// this backend needs.
type limaImageSpec struct {
	Location string `yaml:"location"`
	Arch     string `yaml:"arch"`
}

// limaNetworkSpec mirrors the fields of a Lima template's `networks[]`
// entry this backend needs.
type limaNetworkSpec struct {
	Lima      string `yaml:"lima"`
	Interface string `yaml:"interface"`
}

// limaTemplateSpec is the minimal subset of Lima's VM template schema this
// backend generates - everything else (ssh, mounts, provisioning) is left
// to Lima's own built-in defaults.
type limaTemplateSpec struct {
	Images   []limaImageSpec   `yaml:"images"`
	CPUs     int               `yaml:"cpus"`
	Memory   string            `yaml:"memory"`
	Networks []limaNetworkSpec `yaml:"networks"`
}

// buildLimaYAML generates a per-node Lima template joining networkName,
// pinning both the amd64 and arm64 Ubuntu base images (Lima selects the one
// matching the host architecture automatically).
func buildLimaYAML(networkName string) (string, error) {
	cpus, err := strconv.Atoi(config.NativeCPU)
	if err != nil {
		return "", fmt.Errorf("failed to parse native CPU count %q: %w", config.NativeCPU, err)
	}

	tmpl := limaTemplateSpec{
		Images: []limaImageSpec{
			{Location: config.NativeLimaBaseImageAMD64, Arch: "x86_64"},
			{Location: config.NativeLimaBaseImageARM64, Arch: "aarch64"},
		},
		CPUs:   cpus,
		Memory: config.NativeLimaMemory,
		Networks: []limaNetworkSpec{
			{Lima: networkName, Interface: config.NativeLimaNetworkInterface},
		},
	}

	b, err := yaml.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to marshal lima template: %w", err)
	}

	return string(b), nil
}

// limaIPv4Pattern matches the first IPv4 address in `ip -4 -o addr show`'s
// output, e.g. "... inet 192.168.105.5/24 ...".
var limaIPv4Pattern = regexp.MustCompile(`inet\s+(\d+\.\d+\.\d+\.\d+)/`)

// parseIPv4FromAddrShow extracts the IPv4 address from `ip -4 -o addr show
// dev <interface>`'s output, run inside the guest via Exec.
func parseIPv4FromAddrShow(output string) (string, error) {
	match := limaIPv4Pattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", fmt.Errorf("no IPv4 address found in: %s", strings.TrimSpace(output))
	}
	return match[1], nil
}

// limaNetworkEntry mirrors the relevant fields of a single `limactl network
// list --json` entry.
type limaNetworkEntry struct {
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Gateway string `json:"gateway"`
	NetMask string `json:"netmask"`
}

// parseLimaNetworkListJSON parses `limactl network list --json`'s output:
// one JSON object per line (confirmed from lima-vm/lima's
// cmd/limactl/network.go networkListAction), not a JSON array.
func parseLimaNetworkListJSON(output []byte) ([]limaNetworkEntry, error) {
	var entries []limaNetworkEntry
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry limaNetworkEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse lima network list line %q: %w", line, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read lima network list output: %w", err)
	}

	return entries, nil
}

// subnetsFromNetworkEntries converts limactl's gateway+netmask pairs back
// into network CIDRs, skipping bridged-mode entries which have neither.
func subnetsFromNetworkEntries(entries []limaNetworkEntry) map[string]string {
	subnets := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Gateway == "" || entry.NetMask == "" {
			continue
		}
		cidr, err := cidrFromGatewayNetmask(entry.Gateway, entry.NetMask)
		if err != nil {
			logger.Debugf("skipping lima network %s: %v", entry.Name, err)
			continue
		}
		subnets[entry.Name] = cidr
	}
	return subnets
}

// listNetworks lists all lima networks.
func (c *LimaClient) listNetworks() ([]limaNetworkEntry, error) {
	cmd := exec.Command(limactlBinary, "network", "list", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list lima networks: %w", wrapOutputError(err))
	}

	return parseLimaNetworkListJSON(output)
}

// ListNetworkSubnets returns a map of network name to its IPv4 subnet CIDR.
func (c *LimaClient) ListNetworkSubnets() (map[string]string, error) {
	entries, err := c.listNetworks()
	if err != nil {
		return nil, err
	}

	return subnetsFromNetworkEntries(entries), nil
}

// NetworkExists reports whether a network with the given name already exists.
func (c *LimaClient) NetworkExists(name string) (bool, error) {
	entries, err := c.listNetworks()
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// CreateNetwork creates a lima network in "shared" mode with the given
// subnet CIDR. It is a no-op if the network already exists.
func (c *LimaClient) CreateNetwork(name, subnetCIDR string) error {
	exists, err := c.NetworkExists(name)
	if err != nil {
		return fmt.Errorf("failed to check for existing network %s: %w", name, err)
	}
	if exists {
		logger.Debugf("network %s already exists", name)
		return nil
	}

	gateway, prefix, err := gatewayFromSubnet(subnetCIDR)
	if err != nil {
		return fmt.Errorf("failed to derive gateway for subnet %s: %w", subnetCIDR, err)
	}

	cmd := exec.Command(limactlBinary, "network", "create", name,
		"--mode", config.NativeLimaNetworkMode,
		"--gateway", fmt.Sprintf("%s/%d", gateway, prefix))
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to create network %s: %w: %s", name, err, output)
		}
		return fmt.Errorf("failed to create network %s: %w", name, err)
	}

	logger.Debugf("created network %s with subnet %s", name, subnetCIDR)
	return nil
}

// DeleteNetwork deletes a lima network. Deleting one that doesn't exist is
// treated as success.
func (c *LimaClient) DeleteNetwork(name string) error {
	cmd := exec.Command(limactlBinary, "network", "delete", "--force", name)
	output, err := util.RunCommand(cmd)
	if err != nil {
		if exists, existsErr := c.NetworkExists(name); existsErr == nil && !exists {
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

// limaInstance mirrors the relevant fields of a single `limactl list --json`
// entry (limatype.Instance upstream - not imported as a library, per this
// file's thin-exec-wrapper convention).
type limaInstance struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// parseLimaInstanceListJSON parses `limactl list --json`'s output: one JSON
// object per line, not a JSON array (confirmed from lima-vm/lima's
// cmd/limactl/list.go, which explicitly notes non-TTY output stays in
// "JSON Lines format").
func parseLimaInstanceListJSON(output []byte) ([]limaInstance, error) {
	var instances []limaInstance
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var inst limaInstance
		if err := json.Unmarshal(line, &inst); err != nil {
			return nil, fmt.Errorf("failed to parse lima instance list line %q: %w", line, err)
		}
		instances = append(instances, inst)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read lima instance list output: %w", err)
	}

	return instances, nil
}

// listInstances lists all lima instances (any state).
func (c *LimaClient) listInstances() ([]limaInstance, error) {
	cmd := exec.Command(limactlBinary, "list", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list lima instances: %w", wrapOutputError(err))
	}

	return parseLimaInstanceListJSON(output)
}

// Exec runs a command inside a running node via `limactl shell` and returns
// its stdout. Unlike Incus's `exec`, which runs as root by default, Lima's
// shell connects as the regular, non-root Lima user - callers that need
// root (e.g. installK3s) must prefix their command with sudo themselves.
func (c *LimaClient) Exec(nodeName string, args ...string) (string, error) {
	cmdArgs := append([]string{"shell", nodeName, "--"}, args...)
	cmd := exec.Command(limactlBinary, cmdArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("failed to exec %q on node %s: %w: %s", strings.Join(args, " "), nodeName, err, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("failed to exec %q on node %s: %w", strings.Join(args, " "), nodeName, err)
	}

	return stdout.String(), nil
}

// State returns the current run state of a node, lowercased (e.g. "running")
// to match the shared status-reporting logic in manager.go - Lima reports
// capitalized states ("Running", "Stopped") unlike container/incus.
func (c *LimaClient) State(nodeName string) (string, error) {
	instances, err := c.listInstances()
	if err != nil {
		return "", err
	}

	for _, inst := range instances {
		if inst.Name == nodeName {
			return strings.ToLower(inst.Status), nil
		}
	}

	return "", fmt.Errorf("no instance data returned for node %s", nodeName)
}

// IPv4Address returns the node's routable IPv4 address. `limactl list
// --json` doesn't report a live guest IP for joined networks (its Networks
// field just echoes config, not runtime state), so this queries the guest
// directly for the interface this backend always requests by name in
// buildLimaYAML - network is unused here, kept for interface parity with
// the other two backends, which can disambiguate by it.
func (c *LimaClient) IPv4Address(nodeName, network string) (string, error) {
	output, err := c.Exec(nodeName, "ip", "-4", "-o", "addr", "show", "dev", config.NativeLimaNetworkInterface)
	if err != nil {
		return "", fmt.Errorf("failed to query IPv4 address for node %s: %w", nodeName, err)
	}

	return parseIPv4FromAddrShow(output)
}

// ListNodeNames lists the names of all nodes (any state) whose name starts
// with the given prefix.
func (c *LimaClient) ListNodeNames(prefix string) ([]string, error) {
	instances, err := c.listInstances()
	if err != nil {
		return nil, err
	}

	var names []string
	for _, inst := range instances {
		if strings.HasPrefix(inst.Name, prefix) {
			names = append(names, inst.Name)
		}
	}

	return names, nil
}

// Delete deletes one or more nodes, forcing removal even if still running.
func (c *LimaClient) Delete(nodeNames ...string) error {
	if len(nodeNames) == 0 {
		return nil
	}

	args := append([]string{"delete", "--force"}, nodeNames...)
	cmd := exec.Command(limactlBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to delete nodes %s: %w: %s", strings.Join(nodeNames, ", "), err, output)
		}
		return fmt.Errorf("failed to delete nodes %s: %w", strings.Join(nodeNames, ", "), err)
	}

	return nil
}

// boot writes a generated Lima template to a temp file and boots a node VM
// from it in one step via `limactl start`, which both creates and starts an
// instance when given a template file directly.
func (c *LimaClient) boot(nodeName, networkName string) error {
	yamlContent, err := buildLimaYAML(networkName)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "lok8s-lima-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary lima template for node %s: %w", nodeName, err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(yamlContent); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write lima template for node %s: %w", nodeName, err)
	}
	tmpFile.Close()

	cmd := exec.Command(limactlBinary, "start", "--tty=false", "--name", nodeName, tmpFile.Name())
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to boot node %s: %w: %s", nodeName, err, output)
		}
		return fmt.Errorf("failed to boot node %s: %w", nodeName, err)
	}

	return nil
}

// waitForAgentReady polls a freshly-booted VM until `limactl shell` can
// reach it, mirroring incus_client.go's waitForAgentReady - `limactl start`
// only waits for the VM process itself, not for SSH/cloud-init to finish.
func (c *LimaClient) waitForAgentReady(nodeName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := c.Exec(nodeName, "true")
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timed out after %s waiting for lima guest %s to become reachable: %w", timeout, nodeName, lastErr)
}

// installK3s installs and starts k3s inside an already-booted, reachable
// node via k3s's own install script (the same installK3sScript
// incus_client.go defines). Unlike Incus's `exec`, which runs as root by
// default, Lima's shell connects as the regular Lima user, so this
// explicitly prefixes with sudo; k3sArgs are forwarded positionally via
// "$@", never string-interpolated, for the same injection-safety reason
// documented on installK3sScript itself.
func (c *LimaClient) installK3s(nodeName, k8sVersion string, k3sArgs []string) error {
	v, err := resolveK3sVersion(k8sVersion)
	if err != nil {
		return fmt.Errorf("failed to resolve Kubernetes version: %w", err)
	}

	args := []string{
		"shell", nodeName, "--",
		"sudo", "env", "INSTALL_K3S_VERSION=v" + v,
		"sh", "-c", installK3sScript, "--",
	}
	args = append(args, k3sArgs...)

	cmd := exec.Command(limactlBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to install k3s on %s: %w: %s", nodeName, err, output)
		}
		return fmt.Errorf("failed to install k3s on %s: %w", nodeName, err)
	}

	return nil
}

// BootControlPlane boots a VM and installs+starts a k3s server on it.
func (c *LimaClient) BootControlPlane(nodeName, network, k8sVersion, token, tlsSAN string) error {
	if err := c.boot(nodeName, network); err != nil {
		return err
	}
	if err := c.waitForAgentReady(nodeName, 2*time.Minute); err != nil {
		return err
	}
	if err := makeRootMountShared(c, nodeName); err != nil {
		return err
	}

	return c.installK3s(nodeName, k8sVersion, controlPlaneArgs(token, nodeName, tlsSAN))
}

// BootWorker boots a VM and installs+starts a k3s agent joining serverURL.
func (c *LimaClient) BootWorker(nodeName, network, k8sVersion, serverURL, token string) error {
	if err := c.boot(nodeName, network); err != nil {
		return err
	}
	if err := c.waitForAgentReady(nodeName, 2*time.Minute); err != nil {
		return err
	}
	if err := makeRootMountShared(c, nodeName); err != nil {
		return err
	}

	return c.installK3s(nodeName, k8sVersion, workerArgs(serverURL, token, nodeName))
}
