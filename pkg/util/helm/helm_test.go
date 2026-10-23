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

package helm

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: cluster-a
  cluster:
    server: https://cluster-a.example.com:6443
    insecure-skip-tls-verify: true
- name: cluster-b
  cluster:
    server: https://cluster-b.example.com:6443
    insecure-skip-tls-verify: true
contexts:
- name: context-a
  context:
    cluster: cluster-a
    user: user-a
- name: context-b
  context:
    cluster: cluster-b
    user: user-b
current-context: context-a
users:
- name: user-a
  user: {}
- name: user-b
  user: {}
`

var _ = Describe("HelmManager", func() {
	Describe("GetKubernetesClient", func() {
		var kubeconfigPath string

		BeforeEach(func() {
			tmpDir := GinkgoT().TempDir()
			kubeconfigPath = filepath.Join(tmpDir, "config")
			Expect(os.WriteFile(kubeconfigPath, []byte(testKubeconfig), 0644)).To(Succeed())
		})

		It("uses the context passed to SetContext, not the kubeconfig's current-context", func() {
			hm := NewHelmManager(kubeconfigPath)
			hm.SetContext("context-b")

			client, err := hm.GetKubernetesClient()
			Expect(err).NotTo(HaveOccurred())
			Expect(client.RESTClient().Get().URL().Host).To(Equal("cluster-b.example.com:6443"))
		})

		It("stays pinned even if the kubeconfig's current-context changes afterward", func() {
			hm := NewHelmManager(kubeconfigPath)
			hm.SetContext("context-a")

			// simulate a second, concurrent lok8s process (or another cluster
			// within the same project) flipping current-context in the
			// shared kubeconfig file after this HelmManager was pinned
			updated := strings.Replace(testKubeconfig, "current-context: context-a", "current-context: context-b", 1)
			Expect(os.WriteFile(kubeconfigPath, []byte(updated), 0644)).To(Succeed())

			client, err := hm.GetKubernetesClient()
			Expect(err).NotTo(HaveOccurred())
			Expect(client.RESTClient().Get().URL().Host).To(Equal("cluster-a.example.com:6443"))
		})

		It("falls back to the kubeconfig's current-context when SetContext was never called", func() {
			hm := NewHelmManager(kubeconfigPath)

			client, err := hm.GetKubernetesClient()
			Expect(err).NotTo(HaveOccurred())
			Expect(client.RESTClient().Get().URL().Host).To(Equal("cluster-a.example.com:6443"))
		})
	})
})
