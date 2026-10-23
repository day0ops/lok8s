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

package minikube

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BinaryManager", func() {
	Describe("IsBinaryAvailable", func() {
		It("reports false without downloading when no binary is cached", func() {
			bm := NewBinaryManager()
			bm.SetCacheDir(GinkgoT().TempDir())

			Expect(bm.IsBinaryAvailable()).To(BeFalse())
		})
	})
})

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
			err := manager.installLoadBalancer("proj", 1, &CreateOptions{Project: "proj", NumClusters: 1, InstallMetalLB: false})

			Expect(err).NotTo(HaveOccurred())
			Expect(metallb.installCalled).To(BeFalse())
		})

		It("propagates a MetalLB install failure instead of swallowing it", func() {
			metallb.installErr = errors.New("metallb boom")

			err := manager.installLoadBalancer("proj", 1, &CreateOptions{Project: "proj", NumClusters: 1, InstallMetalLB: true})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metallb boom"))
		})
	})

	Describe("ListProfileNames", func() {
		It("returns no profiles without downloading the binary when it isn't already present", func() {
			bm := NewBinaryManager()
			bm.SetCacheDir(GinkgoT().TempDir())
			manager := &Manager{binaryManager: bm}

			names, err := manager.ListProfileNames()

			Expect(err).NotTo(HaveOccurred())
			Expect(names).To(BeEmpty())
		})
	})

	Describe("minikubeTotalNodes", func() {
		It("adds the control-plane to lok8s's own worker-count NodeCount", func() {
			Expect(minikubeTotalNodes(0)).To(Equal(1))
			Expect(minikubeTotalNodes(1)).To(Equal(2))
			Expect(minikubeTotalNodes(2)).To(Equal(3))
		})
	})
})
