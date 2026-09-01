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

// NodeClient abstracts the platform-specific mechanism used to run node VMs:
// Apple's `container` CLI on macOS (container_client.go), Incus on Linux
// (incus_client.go). NewNodeClient (defined once per platform file, gated by
// a //go:build tag) returns whichever one applies to the host running lok8s.
//
// Each implementation owns turning "boot a control-plane/worker running this
// k3s version" into whatever multi-step dance its platform needs. On macOS
// that's a single `container run` of a rancher/k3s OCI image, since the image
// IS the k3s process. On Linux, Incus VMs boot a general-purpose OS image, so
// BootControlPlane/BootWorker there boot the VM and then install+start k3s
// inside it via its own install script - manager.go and k3s.go don't need to
// know the difference.
type NodeClient interface {
	// CheckPrerequisites verifies the platform's virtualization tooling is
	// installed, at a supported version, and running.
	CheckPrerequisites() error

	// NetworkExists reports whether a network with the given name already exists.
	NetworkExists(name string) (bool, error)
	// ListNetworkSubnets returns a map of network name to its IPv4 subnet CIDR.
	ListNetworkSubnets() (map[string]string, error)
	// CreateNetwork creates a network with the given subnet CIDR. It is a
	// no-op if the network already exists.
	CreateNetwork(name, subnetCIDR string) error
	// DeleteNetwork deletes a network. Deleting one that doesn't exist is
	// treated as success.
	DeleteNetwork(name string) error

	// BootControlPlane boots a node running the given Kubernetes version and
	// starts a k3s server on it, blocking until the node itself (not k3s) is
	// confirmed running. tlsSAN is added to the k3s server certificate's SAN
	// list.
	BootControlPlane(nodeName, network, cpus, memory, k8sVersion, token, tlsSAN string) error
	// BootWorker boots a node running the given Kubernetes version and starts
	// a k3s agent joining serverURL, blocking until the node itself is
	// confirmed running.
	BootWorker(nodeName, network, cpus, memory, k8sVersion, serverURL, token string) error

	// Exec runs a command inside a running node and returns its stdout.
	Exec(nodeName string, args ...string) (string, error)
	// State returns the node's current run state (e.g. "running").
	State(nodeName string) (string, error)
	// IPv4Address returns the node's routable IPv4 address on network.
	IPv4Address(nodeName, network string) (string, error)
	// ListNodeNames lists node names, in any state, starting with prefix.
	ListNodeNames(prefix string) ([]string, error)
	// Delete deletes one or more nodes, forcing removal if still running.
	Delete(nodeNames ...string) error
}
