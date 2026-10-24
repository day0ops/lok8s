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

//go:build linux

package native

// IncusClient is the Linux NodeClient implementation, driving Incus
// (https://linuxcontainers.org/incus/) VMs via its `incus` CLI. Unlike
// Apple's `container` CLI, an Incus VM boots a general-purpose OS image
// rather than an OCI image acting as the workload directly, so
// BootControlPlane/BootWorker here do more work per node: launch the VM,
// wait for its guest agent, then install and start k3s inside it via k3s's
// own install script.
//
// IMPORTANT: this file was written against Incus's documented CLI and API
// behavior, not verified against a real Incus install (this session had no
// Linux host available) - see the parsing/format notes on individual
// functions for the specific things most likely to need adjustment.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util"
	"github.com/day0ops/lok8s/pkg/util/version"
)

// incusBinary is the Incus CLI (https://linuxcontainers.org/incus/).
const incusBinary = "incus"

// installK3sScript downloads and runs k3s's own install script inside a
// freshly-booted node, forwarding installK3s's own trailing arguments to it
// via "$@" rather than any string interpolation - see installK3s for why
// that matters. It also opportunistically installs curl first: Incus's
// `images:` VM images are cloud-oriented but haven't been confirmed to all
// ship curl by default.
const installK3sScript = `
if ! command -v curl >/dev/null 2>&1; then
  apt-get update -qq && apt-get install -y -qq curl
fi
curl -sfL https://get.k3s.io | sh -s - "$@"
`

// incusVersionPattern extracts the first semver-looking token from `incus
// version`'s output (documented as printing e.g. "Client version: 6.0.0").
// Matching loosely rather than an exact label is deliberate, since the exact
// output format hasn't been verified against a real install.
var incusVersionPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// wrapOutputError enriches an error from exec.Cmd.Output() with any captured
// stderr, mirroring container_client.go's macOS equivalent.
func wrapOutputError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}

// IncusClient wraps the `incus` CLI. It is a thin exec wrapper, not a client
// library, mirroring the pattern used for Apple's `container` CLI.
type IncusClient struct{}

// NewNodeClient creates the Linux NodeClient implementation
func NewNodeClient() NodeClient {
	return &IncusClient{}
}

// incusInstanceAddress mirrors the relevant fields of an `incus list
// --format json` entry's state.network.<iface>.addresses[] entries
type incusInstanceAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
}

// incusInstanceNetwork mirrors the relevant fields of an `incus list
// --format json` entry's state.network.<iface> object
type incusInstanceNetwork struct {
	Addresses []incusInstanceAddress `json:"addresses"`
}

// incusInstanceState mirrors the relevant fields of an `incus list --format
// json` entry's state object
type incusInstanceState struct {
	Network map[string]incusInstanceNetwork `json:"network"`
}

// incusInstance mirrors the relevant fields of a single `incus list
// --format json` entry
type incusInstance struct {
	Name   string              `json:"name"`
	Status string              `json:"status"`
	State  *incusInstanceState `json:"state"`
}

// incusNetworkConfig mirrors the relevant fields of an `incus network list
// --format json` entry's config object
type incusNetworkConfig struct {
	IPv4Address string `json:"ipv4.address"`
}

// incusNetworkEntry mirrors the relevant fields of a single `incus network
// list --format json` entry
type incusNetworkEntry struct {
	Name   string             `json:"name"`
	Config incusNetworkConfig `json:"config"`
}

// CheckPrerequisites verifies the `incus` CLI is installed, at a supported
// version, and has at least one storage pool configured (the surest sign
// `incus admin init` has been run - without it, `incus launch` fails).
func (c *IncusClient) CheckPrerequisites() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("the native provider's Incus backend requires Linux, detected %s", runtime.GOOS)
	}

	installedVersion, err := c.Version()
	if err != nil {
		return fmt.Errorf("incus CLI not found or not working: %w (install it via your distro's package manager, see https://linuxcontainers.org/incus/docs/main/installing/)", err)
	}

	if version.Compare(config.NativeLinuxMinIncusVersion, installedVersion) > 0 {
		return fmt.Errorf("incus version %s is too old. Minimum supported version is %s", installedVersion, config.NativeLinuxMinIncusVersion)
	}
	logger.Debugf("using incus version: %s", installedVersion)

	if !c.initialized() {
		return fmt.Errorf("incus has no storage pool configured, run: incus admin init")
	}

	return nil
}

// Version returns the installed `incus` CLI's client version (e.g. "6.0.0")
func (c *IncusClient) Version() (string, error) {
	cmd := exec.Command(incusBinary, "version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run '%s version': %w", incusBinary, wrapOutputError(err))
	}

	if match := incusVersionPattern.FindString(string(output)); match != "" {
		return match, nil
	}

	return "", fmt.Errorf("unable to parse incus version from: %s", strings.TrimSpace(string(output)))
}

// initialized reports whether incus has at least one storage pool configured
func (c *IncusClient) initialized() bool {
	cmd := exec.Command(incusBinary, "storage", "list", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		logger.Debugf("incus storage list check failed: %v", wrapOutputError(err))
		return false
	}

	var pools []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(output, &pools); err != nil {
		logger.Debugf("failed to parse incus storage list output: %v", err)
		return false
	}

	return len(pools) > 0
}

// listNetworks lists all incus networks
func (c *IncusClient) listNetworks() ([]incusNetworkEntry, error) {
	cmd := exec.Command(incusBinary, "network", "list", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list incus networks: %w", wrapOutputError(err))
	}

	var entries []incusNetworkEntry
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse incus network list: %w", err)
	}

	return entries, nil
}

// ListNetworkSubnets returns a map of network name to its IPv4 subnet CIDR.
// Incus stores this as a gateway/prefix pair (e.g. "10.89.0.1/16") rather
// than a bare network CIDR, but net.ParseCIDR handles either form correctly
// for the overlap checks FindFreeSubnet does with this.
func (c *IncusClient) ListNetworkSubnets() (map[string]string, error) {
	entries, err := c.listNetworks()
	if err != nil {
		return nil, err
	}

	subnets := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Config.IPv4Address != "" {
			subnets[entry.Name] = entry.Config.IPv4Address
		}
	}

	return subnets, nil
}

// NetworkExists reports whether a network with the given name already exists
func (c *IncusClient) NetworkExists(name string) (bool, error) {
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

// CreateNetwork creates an incus bridge network with NAT enabled for the
// given subnet CIDR. It is a no-op if the network already exists.
func (c *IncusClient) CreateNetwork(name, subnetCIDR string) error {
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

	cmd := exec.Command(incusBinary, "network", "create", name,
		fmt.Sprintf("ipv4.address=%s/%d", gateway, prefix), "ipv4.nat=true")
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to create network %s: %w: %s", name, err, output)
		}
		return fmt.Errorf("failed to create network %s: %w", name, err)
	}

	logger.Debugf("created network %s with subnet %s", name, subnetCIDR)
	return nil
}

// DeleteNetwork deletes an incus network. Deleting a network that doesn't
// exist is treated as success.
func (c *IncusClient) DeleteNetwork(name string) error {
	cmd := exec.Command(incusBinary, "network", "delete", name)
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

// launch boots a VM from the configured base OS image
func (c *IncusClient) launch(nodeName, network string) error {
	cmd := exec.Command(incusBinary, "launch", config.NativeLinuxBaseImage, nodeName, "--vm",
		"-c", "limits.cpu="+config.NativeCPU,
		"-c", "limits.memory="+config.NativeLinuxMemory,
		"--network", network,
	)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to launch node %s: %w: %s", nodeName, err, output)
		}
		return fmt.Errorf("failed to launch node %s: %w", nodeName, err)
	}

	return nil
}

// waitForAgentReady polls a freshly-launched VM until its incus guest agent
// responds to exec. `incus launch` only waits for the VM process itself to
// start, not for the guest OS to finish booting, so exec can fail for a few
// seconds immediately afterward.
func (c *IncusClient) waitForAgentReady(nodeName string, timeout time.Duration) error {
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

	return fmt.Errorf("timed out after %s waiting for the incus agent on %s to become ready: %w", timeout, nodeName, lastErr)
}

// installK3s installs and starts k3s inside an already-booted, agent-ready
// node via k3s's own install script. k3sArgs (the "server"/"agent" args
// built by controlPlaneArgs/workerArgs) are forwarded using positional
// parameters (`sh -c '...' -- args...`, then the script's own "$@") rather
// than any string interpolation, so arbitrary content in them - ultimately
// derived from user-supplied project/cluster names - can never be
// interpreted as shell syntax. Every argument reaches the k3s binary as a
// separate, literal argv entry no matter what characters it contains.
func (c *IncusClient) installK3s(nodeName, k8sVersion string, k3sArgs []string) error {
	v, err := resolveK3sVersion(k8sVersion)
	if err != nil {
		return fmt.Errorf("failed to resolve Kubernetes version: %w", err)
	}

	// k3s's install script wants its own GitHub release tag verbatim
	// (including the "+"), unlike the Docker image tag used on macOS, which
	// substitutes "+" for "-"
	args := []string{
		"exec", nodeName, "--env", "INSTALL_K3S_VERSION=v" + v,
		"--", "sh", "-c", installK3sScript, "--",
	}
	args = append(args, k3sArgs...)

	cmd := exec.Command(incusBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to install k3s on %s: %w: %s", nodeName, err, output)
		}
		return fmt.Errorf("failed to install k3s on %s: %w", nodeName, err)
	}

	return nil
}

// BootControlPlane boots a VM and installs+starts a k3s server on it.
func (c *IncusClient) BootControlPlane(nodeName, network, k8sVersion, token, tlsSAN string) error {
	if err := c.launch(nodeName, network); err != nil {
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
func (c *IncusClient) BootWorker(nodeName, network, k8sVersion, serverURL, token string) error {
	if err := c.launch(nodeName, network); err != nil {
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

// Exec runs a command inside a running node and returns its stdout
func (c *IncusClient) Exec(nodeName string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", nodeName, "--"}, args...)
	cmd := exec.Command(incusBinary, cmdArgs...)

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

// listInstances lists all incus instances (any state)
func (c *IncusClient) listInstances() ([]incusInstance, error) {
	cmd := exec.Command(incusBinary, "list", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list incus instances: %w", wrapOutputError(err))
	}

	var entries []incusInstance
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse incus instance list: %w", err)
	}

	return entries, nil
}

// State returns the current run state of a node, lowercased (e.g. "running")
// to match the shared status-reporting logic in manager.go, which was
// written against the macOS client's already-lowercase "running"/"stopped".
func (c *IncusClient) State(nodeName string) (string, error) {
	entries, err := c.listInstances()
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.Name == nodeName {
			return strings.ToLower(entry.Status), nil
		}
	}

	return "", fmt.Errorf("no instance data returned for node %s", nodeName)
}

// IPv4Address returns the node's routable IPv4 address. Incus reports
// addresses keyed by guest interface name (e.g. "eth0"), not by the incus
// network resource name, and every node here only ever has one non-loopback
// interface, so the network parameter (kept for interface parity with the
// macOS client, which can disambiguate by it) isn't otherwise used.
func (c *IncusClient) IPv4Address(nodeName, network string) (string, error) {
	entries, err := c.listInstances()
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.Name != nodeName || entry.State == nil {
			continue
		}
		for ifaceName, iface := range entry.State.Network {
			if ifaceName == "lo" {
				continue
			}
			for _, addr := range iface.Addresses {
				if addr.Family == "inet" && addr.Address != "" {
					return addr.Address, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no IPv4 address found for node %s", nodeName)
}

// ListNodeNames lists the names of all nodes (any state) whose name starts
// with the given prefix
func (c *IncusClient) ListNodeNames(prefix string) ([]string, error) {
	entries, err := c.listInstances()
	if err != nil {
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, prefix) {
			names = append(names, entry.Name)
		}
	}

	return names, nil
}

// Delete deletes one or more nodes, forcing removal even if still running
func (c *IncusClient) Delete(nodeNames ...string) error {
	if len(nodeNames) == 0 {
		return nil
	}

	args := append([]string{"delete", "-f"}, nodeNames...)
	cmd := exec.Command(incusBinary, args...)
	if output, err := util.RunCommand(cmd); err != nil {
		if output != "" {
			return fmt.Errorf("failed to delete nodes %s: %w: %s", strings.Join(nodeNames, ", "), err, output)
		}
		return fmt.Errorf("failed to delete nodes %s: %w", strings.Join(nodeNames, ", "), err)
	}

	return nil
}
