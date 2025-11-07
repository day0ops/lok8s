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

package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/day0ops/lok8s/pkg/logger"
)

// UpdateClusterServer updates the server URL for a cluster using Kubernetes SDK
func UpdateClusterServer(clusterName, serverURL string, insecureSkipTLSVerify bool) error {
	logger.Debugf("updating cluster %s server URL to %s", clusterName, serverURL)

	// get kubeconfig path
	kubeconfigPath, err := GetKubeConfigPath()
	if err != nil {
		return err
	}

	// load existing kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// check if cluster exists
	if config.Clusters[clusterName] == nil {
		return fmt.Errorf("cluster %s not found in kubeconfig", clusterName)
	}

	// update cluster server URL
	config.Clusters[clusterName].Server = serverURL
	config.Clusters[clusterName].InsecureSkipTLSVerify = insecureSkipTLSVerify

	// write updated kubeconfig back to file
	err = clientcmd.WriteToFile(*config, kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	logger.Debugf("successfully updated cluster %s server URL to %s", clusterName, serverURL)
	return nil
}

// RenameContext renames a kubectl context using Kubernetes SDK
func RenameContext(oldContext, newContext string) error {
	logger.Infof("⚒️ renaming context %s to %s", oldContext, newContext)

	// get kubeconfig path
	kubeconfigPath, err := GetKubeConfigPath()
	if err != nil {
		return err
	}

	// load existing kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Check if old context exists
	if config.Contexts[oldContext] == nil {
		return fmt.Errorf("context %s not found", oldContext)
	}

	// Check if new context already exists
	if config.Contexts[newContext] != nil {
		logger.Debugf("context %s already exists", newContext)
		return nil
	}

	// Rename the context
	config.Contexts[newContext] = config.Contexts[oldContext]
	delete(config.Contexts, oldContext)

	// Update current context if it was the one being renamed
	if config.CurrentContext == oldContext {
		config.CurrentContext = newContext
	}

	// write updated kubeconfig back to file
	err = clientcmd.WriteToFile(*config, kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	logger.Debugf("successfully renamed context %s to %s", oldContext, newContext)
	return nil
}

// DeleteContext deletes a kubectl context and associated cluster/user using Kubernetes SDK
func DeleteContext(contextName string) error {
	logger.Infof("🚨 deleting context: %s", contextName)

	// get kubeconfig path
	kubeconfigPath, err := GetKubeConfigPath()
	if err != nil {
		return err
	}

	// load existing kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Check if context exists
	context, exists := config.Contexts[contextName]
	if !exists {
		logger.Debugf("context %s not found", contextName)
		return nil
	}

	// Get cluster and usernames from context
	clusterName := context.Cluster
	userName := context.AuthInfo

	// Remove the context
	delete(config.Contexts, contextName)

	// Update current context if it was the one being deleted
	if config.CurrentContext == contextName {
		config.CurrentContext = ""
	}

	// Remove cluster if it exists and is not used by other contexts
	if clusterName != "" {
		clusterInUse := false
		for _, ctx := range config.Contexts {
			if ctx.Cluster == clusterName {
				clusterInUse = true
				break
			}
		}
		if !clusterInUse {
			delete(config.Clusters, clusterName)
			logger.Debugf("removed unused cluster: %s", clusterName)
		}
	}

	// Remove user if it exists and is not used by other contexts
	if userName != "" {
		userInUse := false
		for _, ctx := range config.Contexts {
			if ctx.AuthInfo == userName {
				userInUse = true
				break
			}
		}
		if !userInUse {
			delete(config.AuthInfos, userName)
			logger.Debugf("removed unused user: %s", userName)
		}
	}

	// write updated kubeconfig back to file
	err = clientcmd.WriteToFile(*config, kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	logger.Infof("deleted context: %s, user: %s, cluster: %s", contextName, userName, clusterName)
	return nil
}

// GetKubeConfigPath get the kubeconfig path. First KUBECONFIG is looked at and if not looks at .kube/config
func GetKubeConfigPath() (string, error) {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		// check if the kubeconfig file exists
		if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
			return "", fmt.Errorf("kubeconfig file not found at %s", kubeconfigPath)
		}
	}

	return kubeconfigPath, nil
}
