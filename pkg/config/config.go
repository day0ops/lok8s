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
		"1.34": "v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a",
		"1.33": "v1.33.4@sha256:25a6018e48dfcaee478f4a59af81157a437f15e6e140bf103f85a2e7cd0cbbf2",
		"1.32": "v1.32.8@sha256:abd489f042d2b644e2d033f5c2d900bc707798d075e8186cb65e3f1367a9d5a1",
		"1.31": "v1.31.12@sha256:0f5cc49c5e73c0c2bb6e2df56e7df189240d83cf94edfa30946482eb08ec57d2",
		"1.30": "v1.30.13@sha256:397209b3d947d154f6641f2d0ce8d473732bd91c87d9575ade99049aa33cd648",
		"1.29": "v1.29.14@sha256:8703bd94ee24e51b778d5556ae310c6c0fa67d761fae6379c8e0bb480e6fea29",
	}

	// https://github.com/kubernetes/minikube/blob/master/pkg/minikube/constants/constants.go
	MinikubeK8sVersions = map[string]string{
		"1.34": "1.34.0",
		"1.33": "1.33.1",
		"1.32": "1.32.6",
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
