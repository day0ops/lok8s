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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
)

// k3sAPIPort is the port k3s's embedded API server listens on
const k3sAPIPort = 6443

// generateClusterToken generates a random token used to authenticate node joins.
// The same token is reused for the control-plane and every worker, so no
// separate node-token extraction step is needed.
func generateClusterToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate cluster token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// controlPlaneArgs builds the `k3s server` arguments for the control-plane node.
// --flannel-backend=none and --disable-network-policy hand CNI duties cleanly
// to Cilium; --disable=traefik and --disable=servicelb are required, not
// optional, since k3s's own ServiceLB/Klipper would otherwise fight MetalLB
// for LoadBalancer Services, and Traefik would squat on ports 80/443.
func controlPlaneArgs(token, nodeName, tlsSAN string) []string {
	return []string{
		"server",
		"--token", token,
		"--node-name", nodeName,
		"--flannel-backend=none",
		"--disable-network-policy",
		"--disable=traefik",
		"--disable=servicelb",
		"--write-kubeconfig-mode=644",
		"--tls-san", tlsSAN,
	}
}

// workerArgs builds the `k3s agent` arguments for a worker node
func workerArgs(serverURL, token, nodeName string) []string {
	return []string{
		"agent",
		"--server", serverURL,
		"--token", token,
		"--node-name", nodeName,
	}
}

// waitForInVMReady polls the control-plane node from inside its own VM until
// k3s reports readiness. This works before host-to-VM routing is confirmed
// and gates worker join. k3s images don't ship curl, so this uses the
// embedded kubectl. Note: it must be invoked as the "kubectl" multicall
// binary directly (`kubectl get ...`), not via "k3s kubectl get ..." - the
// latter fails with "unknown command" on the images tested against.
func waitForInVMReady(client *ContainerClient, nodeName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output, err := client.Exec(nodeName, "kubectl", "get", "--raw=/readyz")
		if err == nil && strings.TrimSpace(output) == "ok" {
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timed out after %s waiting for k3s on %s to become ready", timeout, nodeName)
}

// waitForHostReachable polls a plain TCP dial from the host process to the
// control-plane's routable VM IP. This gates kubeconfig extraction and the
// Cilium helm install, since those run on the host via client-go, not inside
// the VM.
func waitForHostReachable(ip string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("timed out after %s waiting for %s to become reachable from host", timeout, addr)
}

// k3sImage resolves a lok8s Kubernetes version (e.g. "1.33" or "stable") to
// the corresponding rancher/k3s image reference
func k3sImage(k8sVersion string) (string, error) {
	version, err := resolveK3sVersion(k8sVersion)
	if err != nil {
		return "", err
	}

	// k3s docker tags use a hyphen where the k3s version itself uses a "+"
	// (e.g. "1.33.13+k3s2" -> "v1.33.13-k3s2")
	tag := "v" + strings.ReplaceAll(version, "+", "-")
	return fmt.Sprintf("rancher/k3s:%s", tag), nil
}

// resolveK3sVersion resolves a Kubernetes minor version (or "stable") to a
// concrete k3s version string from config.NativeK8sVersions
func resolveK3sVersion(k8sVersion string) (string, error) {
	if k8sVersion == "stable" {
		// pick the highest minor deterministically
		var latestMinor, latestVersion string
		for minor, v := range config.NativeK8sVersions {
			if latestMinor == "" || minor > latestMinor {
				latestMinor = minor
				latestVersion = v
			}
		}
		if latestVersion == "" {
			return "", fmt.Errorf("no Kubernetes versions available")
		}
		return latestVersion, nil
	}

	// extract minor version (e.g., "1.33" from "1.33.2")
	parts := strings.Split(k8sVersion, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid Kubernetes version format: %s", k8sVersion)
	}
	minor := fmt.Sprintf("%s.%s", parts[0], parts[1])

	if v, exists := config.NativeK8sVersions[minor]; exists {
		return v, nil
	}

	return "", fmt.Errorf("unsupported Kubernetes version: %s", k8sVersion)
}
