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
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// kubeconfigPath resolves the shared kubeconfig path (KUBECONFIG env var, or
// ~/.kube/config), without requiring the file to already exist. This mirrors
// k8s.GetKubeConfigPath's resolution rules, but that helper errors out when
// the file is missing - which native must tolerate, since it may be the
// first lok8s cluster ever created on this machine.
func kubeconfigPath() (string, error) {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine home directory: %w", err)
	}

	return filepath.Join(home, ".kube", "config"), nil
}

// mergeKubeconfig merges a k3s node's raw kubeconfig - whose cluster, user,
// and context are always named "default" - into the shared kubeconfig under
// tempName, and makes tempName the current context. This mirrors the state
// kind's own cluster creation leaves behind (a fresh "kind-<name>" context,
// already current) so that k8s.RenameContext and k8s.UpdateClusterServer can
// take over from there unmodified.
func mergeKubeconfig(tempName string, raw []byte) error {
	nodeConfig, err := clientcmd.Load(raw)
	if err != nil {
		return fmt.Errorf("failed to parse k3s kubeconfig: %w", err)
	}

	nodeCluster, ok := nodeConfig.Clusters["default"]
	if !ok {
		return fmt.Errorf("k3s kubeconfig is missing the expected \"default\" cluster entry")
	}
	nodeUser, ok := nodeConfig.AuthInfos["default"]
	if !ok {
		return fmt.Errorf("k3s kubeconfig is missing the expected \"default\" user entry")
	}

	path, err := kubeconfigPath()
	if err != nil {
		return err
	}

	sharedConfig, err := loadOrCreateKubeconfig(path)
	if err != nil {
		return err
	}

	sharedConfig.Clusters[tempName] = nodeCluster
	sharedConfig.AuthInfos[tempName] = nodeUser
	sharedConfig.Contexts[tempName] = &clientcmdapi.Context{
		Cluster:  tempName,
		AuthInfo: tempName,
	}
	sharedConfig.CurrentContext = tempName

	if err := clientcmd.WriteToFile(*sharedConfig, path); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}

// clearClusterCA removes the embedded CA certificate data for a cluster
// entry. This is required after k8s.UpdateClusterServer marks a cluster
// insecureSkipTLSVerify: client-go's clientcmd rejects a kubeconfig that
// combines insecure-skip-tls-verify with certificate-authority-data, and
// native's control-plane server address (its routable VM IP) isn't known
// until after boot, so it can't have been part of the certificate's SAN
// list generated at first start.
func clearClusterCA(clusterName string) error {
	path, err := kubeconfigPath()
	if err != nil {
		return err
	}

	sharedConfig, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	cluster, ok := sharedConfig.Clusters[clusterName]
	if !ok {
		return fmt.Errorf("cluster %s not found in kubeconfig", clusterName)
	}
	cluster.CertificateAuthorityData = nil
	cluster.CertificateAuthority = ""

	if err := clientcmd.WriteToFile(*sharedConfig, path); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}

// loadOrCreateKubeconfig loads the kubeconfig at path, or returns a fresh
// empty config (creating the parent directory) if it doesn't exist yet
func loadOrCreateKubeconfig(path string) (*clientcmdapi.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("failed to create kubeconfig directory: %w", err)
		}
		return clientcmdapi.NewConfig(), nil
	}

	sharedConfig, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	return sharedConfig, nil
}
