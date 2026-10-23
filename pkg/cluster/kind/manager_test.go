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

package kind

import (
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeCiliumInstaller is a test double for services.CiliumInstaller.
type fakeCiliumInstaller struct {
	installErr    error
	installCalled bool
}

func (f *fakeCiliumInstaller) InstallCiliumWithValues(clusterName string, extraValues map[string]interface{}) error {
	f.installCalled = true
	return f.installErr
}

// fakeMetalLBInstaller is a test double for services.MetalLBInstaller.
type fakeMetalLBInstaller struct {
	installErr    error
	installCalled bool
}

func (f *fakeMetalLBInstaller) InitializeTracking(project string) error { return nil }

func (f *fakeMetalLBInstaller) InstallMetalLBWithValues(clusterName string, extraValues map[string]interface{}) error {
	f.installCalled = true
	return f.installErr
}

func (f *fakeMetalLBInstaller) ConfigureMetalLB(clusterName, ip string, clusterNumber, totalClusters int, project string) error {
	return nil
}

var _ = Describe("Manager", func() {
	Describe("installCNI", func() {
		var (
			cilium  *fakeCiliumInstaller
			manager *Manager
		)

		BeforeEach(func() {
			cilium = &fakeCiliumInstaller{}
			manager = &Manager{ciliumManager: cilium}
		})

		It("propagates a Cilium install failure instead of swallowing it", func() {
			cilium.installErr = errors.New("cilium boom")

			err := manager.installCNI("ctx", &CreateOptions{CNI: "cilium"})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cilium boom"))
		})

		It("skips Cilium entirely when a different CNI is selected", func() {
			err := manager.installCNI("ctx", &CreateOptions{CNI: "flannel"})

			Expect(err).NotTo(HaveOccurred())
			Expect(cilium.installCalled).To(BeFalse())
		})

		It("succeeds when Cilium installs cleanly", func() {
			err := manager.installCNI("ctx", &CreateOptions{CNI: "cilium"})

			Expect(err).NotTo(HaveOccurred())
			Expect(cilium.installCalled).To(BeTrue())
		})
	})

	Describe("installLoadBalancer", func() {
		var (
			metallb *fakeMetalLBInstaller
			manager *Manager
		)

		BeforeEach(func() {
			metallb = &fakeMetalLBInstaller{}
			manager = &Manager{metallbManager: metallb}
		})

		It("skips MetalLB entirely when not requested", func() {
			err := manager.installLoadBalancer("proj", "ctx", 1, &CreateOptions{Project: "proj", NumClusters: 1, InstallMetalLB: false})

			Expect(err).NotTo(HaveOccurred())
			Expect(metallb.installCalled).To(BeFalse())
		})

		It("propagates a MetalLB install failure instead of swallowing it", func() {
			metallb.installErr = errors.New("metallb boom")

			err := manager.installLoadBalancer("proj", "ctx", 1, &CreateOptions{Project: "proj", NumClusters: 1, InstallMetalLB: true})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metallb boom"))
		})
	})

	Describe("resolveContainerEngine", func() {
		It("returns the preferred engine as-is without auto-detecting", func() {
			engine, err := resolveContainerEngine("podman")

			Expect(err).NotTo(HaveOccurred())
			Expect(engine).To(Equal("podman"))
		})
	})

	Describe("ensureProvider", func() {
		It("returns an error for an unsupported container engine", func() {
			manager := &Manager{}

			err := manager.ensureProvider("bogus-engine")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported container engine"))
		})
	})

	Describe("ciliumRequiresRootfulPodman", func() {
		It("is true only for podman + cilium + rootless", func() {
			Expect(ciliumRequiresRootfulPodman("podman", "cilium", true)).To(BeTrue())
		})

		It("is false for docker even when rootless is somehow reported true", func() {
			Expect(ciliumRequiresRootfulPodman("docker", "cilium", true)).To(BeFalse())
		})

		It("is false for a non-cilium CNI", func() {
			Expect(ciliumRequiresRootfulPodman("podman", "flannel", true)).To(BeFalse())
		})

		It("is false when podman is rootful", func() {
			Expect(ciliumRequiresRootfulPodman("podman", "cilium", false)).To(BeFalse())
		})
	})

	Describe("isSupportedKindCNI", func() {
		It("supports cilium and kindnet", func() {
			Expect(isSupportedKindCNI("cilium")).To(BeTrue())
			Expect(isSupportedKindCNI("kindnet")).To(BeTrue())
		})

		It("rejects flannel and calico, which have no install path for kind", func() {
			Expect(isSupportedKindCNI("flannel")).To(BeFalse())
			Expect(isSupportedKindCNI("calico")).To(BeFalse())
		})
	})

	Describe("createKindConfig", func() {
		var manager *Manager

		BeforeEach(func() {
			manager = &Manager{}
		})

		It("disables the default CNI when cilium is selected", func() {
			configPath, err := manager.createKindConfig("test-cluster", "kindest/node:v1.31.0", 0, 1, "7001", 30000, "cilium")
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(configPath)

			content, err := os.ReadFile(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("disableDefaultCNI: true"))
		})

		It("leaves the default CNI enabled when kindnet is selected", func() {
			configPath, err := manager.createKindConfig("test-cluster", "kindest/node:v1.31.0", 0, 1, "7001", 30000, "kindnet")
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(configPath)

			content, err := os.ReadFile(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).NotTo(ContainSubstring("disableDefaultCNI"))
		})
	})

	Describe("getKindestNodeImage", func() {
		var manager *Manager

		BeforeEach(func() {
			manager = &Manager{}
		})

		It("resolves stable to kind's own current default node image, not a stale minor", func() {
			// matches the default node image announced in the kind v0.33.0
			// release notes (the version pinned in go.mod)
			image, err := manager.getKindestNodeImage("stable")

			Expect(err).NotTo(HaveOccurred())
			Expect(image).To(ContainSubstring("kindest/node:v1.37.0@"))
		})

		It("resolves the newest minor kind v0.33.0 actually pre-builds", func() {
			image, err := manager.getKindestNodeImage("1.37")

			Expect(err).NotTo(HaveOccurred())
			Expect(image).To(ContainSubstring("kindest/node:v1.37.0@"))
		})
	})
})
