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

package services

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/util/helm"
)

var _ = Describe("MetalLBManager", func() {
	var (
		helmManager    *helm.HelmManager
		metallbManager *MetalLBManager
		configManager  *config.ConfigManager
		tempDir        string
	)

	BeforeEach(func() {
		// use a temporary directory for config files to ensure test isolation
		tempDir = GinkgoT().TempDir()
		configManager = config.NewConfigManagerWithDir(tempDir)

		// create a minimal helm manager for testing - use empty kubeconfig path for unit tests
		helmManager = helm.NewHelmManager("")
		metallbManager = NewMetalLBManagerWithOptions(helmManager, 200, 254)
		metallbManager.configManager = configManager

		// ensure clean state for each test
		metallbManager.ipAllocations = make(map[string]*config.MetalLBAllocation)
		metallbManager.usedRanges = make(map[string]bool)
		metallbManager.allNodeIPs = make(map[int]bool)
	})

	Describe("IP Tracking", func() {
		Context("InitializeTracking", func() {
			It("should initialize with empty tracking when no config exists", func() {
				project := "test-project-empty-" + GinkgoT().Name()
				// ensure no config file exists for this project
				_ = configManager.DeleteConfig(project)

				err := metallbManager.InitializeTracking(project)
				Expect(err).NotTo(HaveOccurred())

				Expect(metallbManager.ipAllocations).To(BeEmpty())
				Expect(metallbManager.usedRanges).To(BeEmpty())
				Expect(metallbManager.allNodeIPs).To(BeEmpty())
			})

			It("should load existing allocations from config", func() {
				project := "test-project-load-" + GinkgoT().Name()
				projectConfig := &config.ProjectConfig{
					Project: project,
					MetalLBAllocations: []config.MetalLBAllocation{
						{
							ClusterName: project + "-1",
							IPPrefix:    "192.168.102",
							StartOctet:  200,
							EndOctet:    219,
							NodeIPs:     []int{100, 101},
							IPRange:     "192.168.102.200-192.168.102.219",
						},
					},
				}

				err := configManager.SaveConfig(project, projectConfig)
				Expect(err).NotTo(HaveOccurred())

				err = metallbManager.InitializeTracking(project)
				Expect(err).NotTo(HaveOccurred())

				Expect(metallbManager.ipAllocations).To(HaveLen(1))
				Expect(metallbManager.ipAllocations[project+"-1"]).NotTo(BeNil())
				Expect(metallbManager.ipAllocations[project+"-1"].IPPrefix).To(Equal("192.168.102"))
				Expect(metallbManager.ipAllocations[project+"-1"].StartOctet).To(Equal(200))
				Expect(metallbManager.ipAllocations[project+"-1"].EndOctet).To(Equal(219))

				Expect(metallbManager.usedRanges).To(HaveLen(1))
				// range key format: "ipPrefix.start-end" (e.g., "192.168.102.200-219")
				rangeKey := "192.168.102.200-219"
				Expect(metallbManager.usedRanges[rangeKey]).To(BeTrue())

				Expect(metallbManager.allNodeIPs).To(HaveLen(2))
				Expect(metallbManager.allNodeIPs[100]).To(BeTrue())
				Expect(metallbManager.allNodeIPs[101]).To(BeTrue())
			})

			It("should clear existing tracking before loading", func() {
				project := "test-project-clear-" + GinkgoT().Name()

				// ensure no config file exists for this project (cleanup in BeforeEach should handle this)
				_ = configManager.DeleteConfig(project)

				// set some initial state
				metallbManager.allNodeIPs[100] = true
				metallbManager.usedRanges["192.168.1.200-219"] = true

				err := metallbManager.InitializeTracking(project)
				Expect(err).NotTo(HaveOccurred())

				// should be cleared since no test config exists (BeforeEach cleans up test configs)
				Expect(metallbManager.ipAllocations).To(BeEmpty())
				Expect(metallbManager.usedRanges).To(BeEmpty())
				Expect(metallbManager.allNodeIPs).To(BeEmpty())
			})
		})

		Context("SaveAllocation", func() {
			It("should save allocation to config file", func() {
				project := "test-project-save-" + GinkgoT().Name()
				allocation := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100, 101},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				err := metallbManager.SaveAllocation(project, allocation)
				Expect(err).NotTo(HaveOccurred())

				// verify config file was created
				configPath := configManager.GetConfigPath(project)
				Expect(configPath).To(BeAnExistingFile())

				// verify allocation was saved
				projectConfig, err := configManager.LoadConfig(project)
				Expect(err).NotTo(HaveOccurred())
				Expect(projectConfig.MetalLBAllocations).To(HaveLen(1))
				Expect(projectConfig.MetalLBAllocations[0].ClusterName).To(Equal(project + "-1"))
				Expect(projectConfig.MetalLBAllocations[0].IPRange).To(Equal("192.168.102.200-192.168.102.219"))
			})

			It("should update existing allocation for same cluster", func() {
				project := "test-project-update-" + GinkgoT().Name()
				allocation1 := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				err := metallbManager.SaveAllocation(project, allocation1)
				Expect(err).NotTo(HaveOccurred())

				// update allocation
				allocation2 := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100, 101, 102},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				err = metallbManager.SaveAllocation(project, allocation2)
				Expect(err).NotTo(HaveOccurred())

				// verify only one allocation exists (updated)
				projectConfig, err := configManager.LoadConfig(project)
				Expect(err).NotTo(HaveOccurred())
				Expect(projectConfig.MetalLBAllocations).To(HaveLen(1))
				Expect(projectConfig.MetalLBAllocations[0].NodeIPs).To(HaveLen(3))
				Expect(projectConfig.MetalLBAllocations[0].NodeIPs).To(ContainElement(102))
			})

			It("should add multiple allocations for different clusters", func() {
				project := "test-project-multi-" + GinkgoT().Name()
				allocation1 := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				allocation2 := &config.MetalLBAllocation{
					ClusterName: project + "-2",
					IPPrefix:    "192.168.102",
					StartOctet:  220,
					EndOctet:    239,
					NodeIPs:     []int{101},
					IPRange:     "192.168.102.220-192.168.102.239",
				}

				err := metallbManager.SaveAllocation(project, allocation1)
				Expect(err).NotTo(HaveOccurred())

				err = metallbManager.SaveAllocation(project, allocation2)
				Expect(err).NotTo(HaveOccurred())

				// verify both allocations exist
				projectConfig, err := configManager.LoadConfig(project)
				Expect(err).NotTo(HaveOccurred())
				Expect(projectConfig.MetalLBAllocations).To(HaveLen(2))
			})

			It("should update in-memory tracking after saving", func() {
				project := "test-project-tracking-" + GinkgoT().Name()
				allocation := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100, 101},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				err := metallbManager.SaveAllocation(project, allocation)
				Expect(err).NotTo(HaveOccurred())

				// verify in-memory tracking
				Expect(metallbManager.ipAllocations[project+"-1"]).NotTo(BeNil())
				rangeKey := "192.168.102.200-219"
				Expect(metallbManager.usedRanges[rangeKey]).To(BeTrue())
				Expect(metallbManager.allNodeIPs[100]).To(BeTrue())
				Expect(metallbManager.allNodeIPs[101]).To(BeTrue())
			})

			It("should create config file if it doesn't exist", func() {
				project := "new-project-create-" + GinkgoT().Name()
				allocation := &config.MetalLBAllocation{
					ClusterName: project + "-1",
					IPPrefix:    "192.168.102",
					StartOctet:  200,
					EndOctet:    219,
					NodeIPs:     []int{100},
					IPRange:     "192.168.102.200-192.168.102.219",
				}

				// verify config file doesn't exist initially
				configPath := configManager.GetConfigPath(project)
				// Note: config file might not exist, which is expected
				// The test verifies that SaveAllocation creates it

				err := metallbManager.SaveAllocation(project, allocation)
				Expect(err).NotTo(HaveOccurred())

				// verify config file was created
				Expect(configPath).To(BeAnExistingFile())
			})
		})
	})

	Describe("IP Range Generation", func() {
		Context("generateMetalLBIPRange", func() {
			It("should extract IP prefix correctly", func() {
				// this is a unit test that would require mocking k8s client
				// for now, we'll test the logic indirectly through integration tests
				Skip("Requires k8s client mocking - covered by e2e tests")
			})
		})
	})

	Describe("Manager initialization", func() {
		Context("NewMetalLBManager", func() {
			It("should create manager with default settings", func() {
				manager := NewMetalLBManager(helmManager)
				Expect(manager).NotTo(BeNil())
				Expect(manager.helmManager).To(Equal(helmManager))
				Expect(manager.configManager).NotTo(BeNil())
				Expect(manager.ipAllocations).NotTo(BeNil())
				Expect(manager.usedRanges).NotTo(BeNil())
				Expect(manager.allNodeIPs).NotTo(BeNil())
			})
		})

		Context("NewMetalLBManagerWithOptions", func() {
			It("should create manager with custom octet ranges", func() {
				manager := NewMetalLBManagerWithOptions(helmManager, 200, 254)
				Expect(manager).NotTo(BeNil())
				Expect(manager.minOctetRange).To(Equal(200))
				Expect(manager.maxOctetRange).To(Equal(254))
				Expect(manager.configManager).NotTo(BeNil())
				Expect(manager.ipAllocations).NotTo(BeNil())
				Expect(manager.usedRanges).NotTo(BeNil())
				Expect(manager.allNodeIPs).NotTo(BeNil())
			})
		})
	})
})
