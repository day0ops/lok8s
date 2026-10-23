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
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeCiliumInstaller is a test double for services.CiliumInstaller.
type fakeCiliumInstaller struct {
	installErr     error
	installCalled  bool
	receivedValues map[string]interface{}
}

func (f *fakeCiliumInstaller) InstallCiliumWithValues(clusterName string, extraValues map[string]interface{}) error {
	f.installCalled = true
	f.receivedValues = extraValues
	return f.installErr
}

// fakeMetalLBInstaller is a test double for services.MetalLBInstaller.
type fakeMetalLBInstaller struct {
	initErr         error
	installErr      error
	configureErr    error
	installCalled   bool
	configureCalled bool
}

func (f *fakeMetalLBInstaller) InitializeTracking(project string) error {
	return f.initErr
}

func (f *fakeMetalLBInstaller) InstallMetalLBWithValues(clusterName string, extraValues map[string]interface{}) error {
	f.installCalled = true
	return f.installErr
}

func (f *fakeMetalLBInstaller) ConfigureMetalLB(clusterName, ip string, clusterNumber, totalClusters int, project string) error {
	f.configureCalled = true
	return f.configureErr
}

// fakeNodeClient is a test double for NodeClient, sufficient for exercising
// manager.go's orchestration logic without any real VM tooling.
type fakeNodeClient struct {
	nodeNames   []string
	deletedCall []string
	deleteErr   error
	listErr     error
}

func (f *fakeNodeClient) CheckPrerequisites() error { return nil }
func (f *fakeNodeClient) NetworkExists(name string) (bool, error) {
	return false, nil
}
func (f *fakeNodeClient) ListNetworkSubnets() (map[string]string, error) { return nil, nil }
func (f *fakeNodeClient) CreateNetwork(name, subnetCIDR string) error    { return nil }
func (f *fakeNodeClient) DeleteNetwork(name string) error                { return nil }
func (f *fakeNodeClient) BootControlPlane(nodeName, network, k8sVersion, token, tlsSAN string) error {
	return nil
}
func (f *fakeNodeClient) BootWorker(nodeName, network, k8sVersion, serverURL, token string) error {
	return nil
}
func (f *fakeNodeClient) Exec(nodeName string, args ...string) (string, error) { return "", nil }
func (f *fakeNodeClient) State(nodeName string) (string, error)                { return "", nil }
func (f *fakeNodeClient) IPv4Address(nodeName, network string) (string, error) { return "", nil }
func (f *fakeNodeClient) ListNodeNames(prefix string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var matched []string
	for _, n := range f.nodeNames {
		if strings.HasPrefix(n, prefix) {
			matched = append(matched, n)
		}
	}
	return matched, nil
}
func (f *fakeNodeClient) Delete(nodeNames ...string) error {
	f.deletedCall = append(f.deletedCall, nodeNames...)
	return f.deleteErr
}

var _ = Describe("Manager", func() {
	Describe("installCNIAndLoadBalancer", func() {
		var (
			cilium  *fakeCiliumInstaller
			metallb *fakeMetalLBInstaller
			manager *Manager
			opts    *CreateOptions
		)

		BeforeEach(func() {
			cilium = &fakeCiliumInstaller{}
			metallb = &fakeMetalLBInstaller{}
			manager = &Manager{ciliumManager: cilium, metallbManager: metallb}
			opts = &CreateOptions{Project: "proj", NumClusters: 1, InstallMetalLB: true}
		})

		It("propagates a Cilium install failure instead of swallowing it", func() {
			cilium.installErr = errors.New("cilium boom")

			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cilium boom"))
			Expect(metallb.installCalled).To(BeFalse(), "MetalLB must not be attempted after a Cilium failure")
		})

		It("propagates a MetalLB install failure instead of swallowing it", func() {
			metallb.installErr = errors.New("metallb boom")

			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metallb boom"))
		})

		It("propagates a MetalLB configure failure instead of swallowing it", func() {
			metallb.configureErr = errors.New("configure boom")

			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("configure boom"))
		})

		It("skips MetalLB entirely when not requested", func() {
			opts.InstallMetalLB = false

			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).NotTo(HaveOccurred())
			Expect(metallb.installCalled).To(BeFalse())
		})

		It("succeeds when both Cilium and MetalLB install cleanly", func() {
			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).NotTo(HaveOccurred())
			Expect(cilium.installCalled).To(BeTrue())
			Expect(metallb.installCalled).To(BeTrue())
			Expect(metallb.configureCalled).To(BeTrue())
		})

		It("enables eBPF masquerade, without which cross-cluster pod traffic silently hangs", func() {
			err := manager.installCNIAndLoadBalancer("ctx", "10.0.0.2", 1, opts)

			Expect(err).NotTo(HaveOccurred())
			Expect(cilium.receivedValues).To(HaveKeyWithValue("bpf", map[string]interface{}{"masquerade": true}))
		})
	})

	Describe("cleanupFailedCluster", func() {
		var (
			client  *fakeNodeClient
			manager *Manager
		)

		BeforeEach(func() {
			// isolate from the real kubeconfig: k8s.DeleteContext resolves its
			// path via $KUBECONFIG, which must never point at the user's own
			// ~/.kube/config during a test
			tmpDir := GinkgoT().TempDir()
			kubeconfigPath := filepath.Join(tmpDir, "config")
			Expect(os.WriteFile(kubeconfigPath, []byte("apiVersion: v1\nkind: Config\n"), 0644)).To(Succeed())
			GinkgoT().Setenv("KUBECONFIG", kubeconfigPath)

			client = &fakeNodeClient{nodeNames: []string{"proj-control-plane", "proj-worker-1", "other-control-plane"}}
			manager = &Manager{client: client}
		})

		It("deletes only the nodes belonging to the failed cluster", func() {
			manager.cleanupFailedCluster("proj", "proj")

			Expect(client.deletedCall).To(ConsistOf("proj-control-plane", "proj-worker-1"))
		})

		It("does not call Delete when the cluster has no nodes", func() {
			client.nodeNames = nil

			manager.cleanupFailedCluster("proj", "proj")

			Expect(client.deletedCall).To(BeEmpty())
		})
	})
})
