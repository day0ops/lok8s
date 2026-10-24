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
	"runtime"
	"strings"
)

const (
	// application info
	AppName = "lok8s"

	// network defaults
	DefaultNetworkSubnetCIDR = "10.89.0.0/16"

	// cluster level defaults
	DefaultClusterNum = 1
	DefaultNodeCount  = 2

	// Kind defaults
	KindNetworkName      = "kind"
	KindNetworkGatewayIP = "10.89.0.1"
	KindRegistryName     = "kind-registry"
	KindRegistryPort     = 5000
	KindControlPlanePort = 7000

	// Minikube defaults
	MinikubeCPU                   = "4"
	MinikubeMemory                = "8GiB"
	MinikubeDiskSize              = "10GiB"
	MinikubeVmnetNetworkName      = "vmnet-shared"
	MinikubeLibvirtPvtNetworkName = "minikube-net"
	MinikubeDefaultBridgeNetName  = "virbr50"
	MinikubeQemuSystem            = "qemu:///system"
	MinikubeNetworkDHCPIPCount    = 2000
	// MinikubeServiceIPRangeBase is the base IP range for service cluster IP ranges
	// Format: 10.255.{clusterIndex}.0/24
	MinikubeServiceIPRangeBase = "10.255"

	// MetalLB defaults
	MetalLBRangeMinLastOctet = 200
	MetalLBRangeMaxLastOctet = 254

	// vfkit minimum supported version (macOS)
	VfkitMinSupportedVersion = "0.6.1"

	// Minikube minimum supported version
	MinikubeMinSupportedVersion = "1.36.0"

	CloudProviderKindMinSupportedVersion = "0.8.0"

	// Native (Apple Virtualization / `container` CLI) defaults
	NativeContainerMinSupportedVersion = "1.2.1"
	NativeNetworkNamePrefix            = "lok8s-"
	NativeCPU                          = "2"
	NativeMemory                       = "2G"

	// Native on Linux (Incus) defaults. Unlike the macOS side, these haven't
	// been verified against a real Incus install (see incus_client.go) - the
	// version floor and image reference are best-effort defaults, not
	// confirmed-working values.
	NativeLinuxMinIncusVersion = "6.0"
	NativeLinuxBaseImage       = "images:debian/12"
	// NativeLinuxMemory uses Incus's "GiB" unit syntax; NativeCPU above is
	// reused as-is since a bare number is valid for both platforms.
	NativeLinuxMemory = "2GiB"

	// Native on Lima defaults. Lima runs identically on macOS and Linux via
	// the same limactl CLI, unlike the OS-specific container/incus backends.
	// NativeLimaMinSupportedVersion is the version floor Lima's own
	// templates/ubuntu-24.04.yaml template declares as its minimum.
	NativeLimaMinSupportedVersion = "2.0.0"
	// NativeLimaNetworkMode is always "shared": the one Lima network mode
	// that gives every node a routable IP on a project-wide subnet, backed
	// by socket_vmnet on macOS and Lima's bundled privileged-network helper
	// on Linux.
	NativeLimaNetworkMode = "shared"
	// NativeLimaNetworkInterface is the guest interface name requested for
	// the project network, so IPv4Address can reliably query it by name
	// instead of guessing which interface the guest assigned it.
	NativeLimaNetworkInterface = "lima0"
	// NativeLimaMemory uses Lima's own size-string syntax (same syntax as
	// Incus's NativeLinuxMemory); NativeCPU above is reused as-is since a
	// bare number is valid for Lima's integer `cpus:` field too.
	NativeLimaMemory = "2GiB"
	// NativeLimaBaseImageAMD64/ARM64 pin a stable, non-dated Ubuntu 24.04
	// "release" cloud image URL (Canonical keeps the content behind it
	// current), not a specific dated/digest-pinned snapshot - the same
	// "rolling alias, not a hash" choice NativeLinuxBaseImage already makes.
	NativeLimaBaseImageAMD64 = "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img"
	NativeLimaBaseImageARM64 = "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-arm64.img"

	// LibVirt network template
	NetworkTemplate = `
<network>
  <name>{{.Name}}</name>
  <dns enable='no'/>
  <bridge name='{{.Bridge}}' stp='on' delay='0'/>
  {{- with .Parameters}}
  <ip address='{{.Gateway}}' netmask='{{.Netmask}}'>
    <dhcp>
      <range start='{{.ClientMin}}' end='{{.ClientMax}}'/>
    </dhcp>
  </ip>
  {{- end}}
</network>
`
)

var (
	// Kubernetes version mappings
	// refer to https://github.com/kubernetes-sigs/kind/releases for the latest builds
	KindK8sVersions = map[string]string{
		"1.37": "v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5",
		"1.36": "v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed",
		"1.35": "v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0",
		"1.34": "v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d",
		"1.33": "v1.33.4@sha256:25a6018e48dfcaee478f4a59af81157a437f15e6e140bf103f85a2e7cd0cbbf2",
		"1.32": "v1.32.8@sha256:abd489f042d2b644e2d033f5c2d900bc707798d075e8186cb65e3f1367a9d5a1",
		"1.31": "v1.31.12@sha256:0f5cc49c5e73c0c2bb6e2df56e7df189240d83cf94edfa30946482eb08ec57d2",
		"1.30": "v1.30.13@sha256:397209b3d947d154f6641f2d0ce8d473732bd91c87d9575ade99049aa33cd648",
		"1.29": "v1.29.14@sha256:8703bd94ee24e51b778d5556ae310c6c0fa67d761fae6379c8e0bb480e6fea29",
	}

	// https://github.com/kubernetes/minikube/blob/master/pkg/minikube/constants/constants.go
	MinikubeK8sVersions = map[string]string{
		"1.37": "1.37.1",
		"1.36": "1.36.5",
		"1.35": "1.35.1",
		"1.34": "1.34.0",
		"1.33": "1.33.1",
		"1.32": "1.32.6",
	}

	// k3s image tags, see https://github.com/k3s-io/k3s/releases
	NativeK8sVersions = map[string]string{
		"1.37": "1.37.1+k3s1",
		"1.36": "1.36.5+k3s1",
		"1.35": "1.35.9+k3s1",
		"1.34": "1.34.12+k3s1",
		"1.33": "1.33.13+k3s2",
	}

	KindRegistries = map[string]string{
		"docker":             "https://registry-1.docker.io",
		"us-docker":          "https://us-docker.pkg.dev",
		"us-central1-docker": "https://us-central1-docker.pkg.dev",
		"quay":               "https://quay.io",
		"gcr":                "https://gcr.io",
	}
)

// GetOS returns the current operating system
func GetOS() string {
	return runtime.GOOS
}

// GetArch returns the current architecture
func GetArch() string {
	return runtime.GOARCH
}

// IsLinux returns true if running on Linux
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

// IsDarwin returns true if running on macOS
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}

// GetMinikubeServiceIPRange returns the service cluster IP range for a given cluster index
// Format: 10.255.{clusterIndex}.0/24
// Example: clusterIndex 1 -> "10.255.1.0/24", clusterIndex 2 -> "10.255.2.0/24"
func GetMinikubeServiceIPRange(clusterIndex int) string {
	twoDigits := fmt.Sprintf("%02d", clusterIndex)
	indexStr := strings.TrimLeft(twoDigits, "0")
	if indexStr == "" {
		indexStr = "0"
	}
	return fmt.Sprintf("%s.%s.0/24", MinikubeServiceIPRangeBase, indexStr)
}
