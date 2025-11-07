package config

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ProjectConfig", func() {
	Describe("ConfigManager", func() {
		Context("Creation", func() {
			It("should create a valid ConfigManager", func() {
				cm := NewConfigManager()
				Expect(cm).NotTo(BeNil())
				Expect(cm.configDir).NotTo(BeEmpty())
			})

			It("should set config directory under home directory", func() {
				cm := NewConfigManager()
				homeDir, err := os.UserHomeDir()
				if err != nil {
					Skip("Cannot get home directory for test")
				}

				expectedConfigDir := filepath.Join(homeDir, "."+AppName)
				Expect(cm.configDir).To(Equal(expectedConfigDir))
			})
		})

		Context("Config path generation", func() {
			It("should generate correct config path", func() {
				cm := NewConfigManager()
				project := "test-project"

				configPath := cm.GetConfigPath(project)
				expectedPath := filepath.Join(cm.configDir, project+".yaml")

				Expect(configPath).To(Equal(expectedPath))
			})
		})

		Context("Config file operations", func() {
			var (
				tempDir string
				cm      *ConfigManager
			)

			BeforeEach(func() {
				tempDir = GinkgoT().TempDir()
				cm = &ConfigManager{configDir: tempDir}
			})

			Context("Save and load config", func() {
				It("should save and load config correctly", func() {
					project := "test-project"
					config := &ProjectConfig{
						Project:              project,
						Environment:          "kind",
						NumClusters:          2,
						NodeCount:            3,
						K8sVersion:           "v1.28.0",
						GatewayIP:            "10.89.0.1",
						SubnetCIDR:           "10.89.0.0/16",
						CNI:                  "cilium",
						ContainerRuntime:     "containerd",
						InstallMetalLB:       false,
						InstallCloudProvider: true,
						SkipMetalLB:          true,
					}

					// Save config
					err := cm.SaveConfig(project, config)
					Expect(err).NotTo(HaveOccurred())

					// Verify file was created
					configPath := cm.GetConfigPath(project)
					Expect(configPath).To(BeAnExistingFile())

					// Load config
					loadedConfig, err := cm.LoadConfig(project)
					Expect(err).NotTo(HaveOccurred())
					Expect(loadedConfig).NotTo(BeNil())

					// Verify loaded config matches saved config
					Expect(loadedConfig.Project).To(Equal(config.Project))
					Expect(loadedConfig.Environment).To(Equal(config.Environment))
					Expect(loadedConfig.NumClusters).To(Equal(config.NumClusters))
					Expect(loadedConfig.NodeCount).To(Equal(config.NodeCount))
					Expect(loadedConfig.CNI).To(Equal(config.CNI))
					Expect(loadedConfig.InstallMetalLB).To(Equal(config.InstallMetalLB))
					Expect(loadedConfig.InstallCloudProvider).To(Equal(config.InstallCloudProvider))
				})

				It("should save and load config with MetalLB allocations", func() {
					project := "test-project-metallb"
					config := &ProjectConfig{
						Project:     project,
						Environment: "kind",
						NumClusters: 2,
						NodeCount:   3,
						K8sVersion:  "v1.28.0",
						MetalLBAllocations: []MetalLBAllocation{
							{
								ClusterName: "test-project-1",
								IPPrefix:    "192.168.102",
								StartOctet:  200,
								EndOctet:    219,
								NodeIPs:     []int{100, 101},
								IPRange:     "192.168.102.200-192.168.102.219",
							},
							{
								ClusterName: "test-project-2",
								IPPrefix:    "192.168.102",
								StartOctet:  220,
								EndOctet:    239,
								NodeIPs:     []int{102, 103},
								IPRange:     "192.168.102.220-192.168.102.239",
							},
						},
					}

					// Save config
					err := cm.SaveConfig(project, config)
					Expect(err).NotTo(HaveOccurred())

					// Load config
					loadedConfig, err := cm.LoadConfig(project)
					Expect(err).NotTo(HaveOccurred())
					Expect(loadedConfig).NotTo(BeNil())

					// Verify MetalLB allocations
					Expect(loadedConfig.MetalLBAllocations).To(HaveLen(2))
					Expect(loadedConfig.MetalLBAllocations[0].ClusterName).To(Equal("test-project-1"))
					Expect(loadedConfig.MetalLBAllocations[0].IPPrefix).To(Equal("192.168.102"))
					Expect(loadedConfig.MetalLBAllocations[0].StartOctet).To(Equal(200))
					Expect(loadedConfig.MetalLBAllocations[0].EndOctet).To(Equal(219))
					Expect(loadedConfig.MetalLBAllocations[0].NodeIPs).To(Equal([]int{100, 101}))
					Expect(loadedConfig.MetalLBAllocations[0].IPRange).To(Equal("192.168.102.200-192.168.102.219"))

					Expect(loadedConfig.MetalLBAllocations[1].ClusterName).To(Equal("test-project-2"))
					Expect(loadedConfig.MetalLBAllocations[1].IPPrefix).To(Equal("192.168.102"))
					Expect(loadedConfig.MetalLBAllocations[1].StartOctet).To(Equal(220))
					Expect(loadedConfig.MetalLBAllocations[1].EndOctet).To(Equal(239))
					Expect(loadedConfig.MetalLBAllocations[1].NodeIPs).To(Equal([]int{102, 103}))
					Expect(loadedConfig.MetalLBAllocations[1].IPRange).To(Equal("192.168.102.220-192.168.102.239"))
				})
			})

			Context("Load non-existent config", func() {
				It("should return nil for non-existent project", func() {
					project := "non-existent-project"
					config, err := cm.LoadConfig(project)

					Expect(err).NotTo(HaveOccurred())
					Expect(config).To(BeNil())
				})
			})

			Context("Delete config", func() {
				It("should delete existing config", func() {
					project := "test-project"
					config := &ProjectConfig{
						Project:     project,
						Environment: "kind",
						NumClusters: 1,
					}

					// Save config first
					err := cm.SaveConfig(project, config)
					Expect(err).NotTo(HaveOccurred())

					// Verify file exists
					configPath := cm.GetConfigPath(project)
					Expect(configPath).To(BeAnExistingFile())

					// Delete config
					err = cm.DeleteConfig(project)
					Expect(err).NotTo(HaveOccurred())

					// Verify file was deleted
					Expect(configPath).NotTo(BeAnExistingFile())
				})

				It("should handle deletion of non-existent config gracefully", func() {
					project := "non-existent-project"
					err := cm.DeleteConfig(project)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Context("List configs", func() {
				It("should list all configs", func() {
					// Create multiple config files
					projects := []string{"project1", "project2", "project3"}
					for _, project := range projects {
						config := &ProjectConfig{
							Project:     project,
							Environment: "kind",
							NumClusters: 1,
						}
						err := cm.SaveConfig(project, config)
						Expect(err).NotTo(HaveOccurred())
					}

					// List configs
					listedProjects, err := cm.ListConfigs()
					Expect(err).NotTo(HaveOccurred())
					Expect(listedProjects).To(HaveLen(len(projects)))

					// Verify all projects are listed
					for _, project := range projects {
						Expect(listedProjects).To(ContainElement(project))
					}
				})

				It("should return empty list when no configs exist", func() {
					projects, err := cm.ListConfigs()
					Expect(err).NotTo(HaveOccurred())
					Expect(projects).To(BeEmpty())
				})
			})
		})
	})

	Describe("User-defined config files", func() {
		Context("LoadConfigFromFile", func() {
			var tempDir string

			BeforeEach(func() {
				tempDir = GinkgoT().TempDir()
			})

			It("should load valid YAML config file", func() {
				configFile := filepath.Join(tempDir, "test-config.yaml")

				// Create a valid YAML config file
				yamlContent := `project: "test-project"
environment: "kind"
num_clusters: 2
node_count: 3
kubernetes_version: "v1.28.0"
gateway_ip: "10.89.0.1"
subnet_cidr: "10.89.0.0/16"
cni: "cilium"
container_runtime: "containerd"
install_metallb: false
install_cloud_provider: true
skip_metallb: true`

				err := os.WriteFile(configFile, []byte(yamlContent), 0644)
				Expect(err).NotTo(HaveOccurred())

				// Load config from file
				config, err := LoadConfigFromFile(configFile)
				Expect(err).NotTo(HaveOccurred())
				Expect(config).NotTo(BeNil())

				// Verify loaded values
				Expect(config.Project).To(Equal("test-project"))
				Expect(config.Environment).To(Equal("kind"))
				Expect(config.NumClusters).To(Equal(2))
				Expect(config.NodeCount).To(Equal(3))
				Expect(config.CNI).To(Equal("cilium"))
				Expect(config.InstallMetalLB).To(BeFalse())
				Expect(config.InstallCloudProvider).To(BeTrue())
			})

			It("should return error for non-existent file", func() {
				configFile := "/non/existent/file.yaml"

				config, err := LoadConfigFromFile(configFile)
				Expect(err).To(HaveOccurred())
				Expect(config).To(BeNil())
			})

			It("should return error for invalid YAML", func() {
				configFile := filepath.Join(tempDir, "invalid-config.yaml")

				// Create invalid YAML content
				invalidYAML := `project: "test"
invalid: yaml: content: [`

				err := os.WriteFile(configFile, []byte(invalidYAML), 0644)
				Expect(err).NotTo(HaveOccurred())

				config, err := LoadConfigFromFile(configFile)
				Expect(err).To(HaveOccurred())
				Expect(config).To(BeNil())
			})
		})
	})

	Describe("Config merging", func() {
		Context("MergeConfigs", func() {
			var (
				base     *ProjectConfig
				override *ProjectConfig
			)

			BeforeEach(func() {
				base = &ProjectConfig{
					Project:              "base-project",
					Environment:          "kind",
					NumClusters:          1,
					NodeCount:            2,
					K8sVersion:           "v1.27.0",
					GatewayIP:            "10.89.0.1",
					SubnetCIDR:           "10.89.0.0/16",
					Bridge:               "virbr50",
					CPU:                  "2",
					Memory:               "4GiB",
					DiskSize:             "5GiB",
					CNI:                  "calico",
					ContainerRuntime:     "docker",
					InstallMetalLB:       true,
					InstallCloudProvider: false,
					SkipMetalLB:          false,
				}
			})

			Context("Complete override", func() {
				BeforeEach(func() {
					override = &ProjectConfig{
						Project:              "override-project",
						Environment:          "minikube",
						NumClusters:          3,
						NodeCount:            4,
						K8sVersion:           "v1.28.0",
						GatewayIP:            "10.100.0.1",
						SubnetCIDR:           "10.100.0.0/16",
						Bridge:               "virbr100",
						CPU:                  "8",
						Memory:               "16GiB",
						DiskSize:             "20GiB",
						CNI:                  "cilium",
						ContainerRuntime:     "containerd",
						InstallMetalLB:       false,
						InstallCloudProvider: true,
						SkipMetalLB:          true,
					}
				})

				It("should override all fields", func() {
					merged := MergeConfigs(base, override)

					Expect(merged.Project).To(Equal(override.Project))
					Expect(merged.Environment).To(Equal(override.Environment))
					Expect(merged.NumClusters).To(Equal(override.NumClusters))
					Expect(merged.NodeCount).To(Equal(override.NodeCount))
					Expect(merged.K8sVersion).To(Equal(override.K8sVersion))
					Expect(merged.GatewayIP).To(Equal(override.GatewayIP))
					Expect(merged.SubnetCIDR).To(Equal(override.SubnetCIDR))
					Expect(merged.Bridge).To(Equal(override.Bridge))
					Expect(merged.CPU).To(Equal(override.CPU))
					Expect(merged.Memory).To(Equal(override.Memory))
					Expect(merged.DiskSize).To(Equal(override.DiskSize))
					Expect(merged.CNI).To(Equal(override.CNI))
					Expect(merged.ContainerRuntime).To(Equal(override.ContainerRuntime))
					Expect(merged.InstallMetalLB).To(Equal(override.InstallMetalLB))
					Expect(merged.InstallCloudProvider).To(Equal(override.InstallCloudProvider))
					Expect(merged.SkipMetalLB).To(Equal(override.SkipMetalLB))
				})
			})

			Context("Partial override", func() {
				BeforeEach(func() {
					override = &ProjectConfig{
						Project:              "", // Empty - should not override
						Environment:          "minikube",
						NumClusters:          0, // Zero - should not override
						NodeCount:            4,
						K8sVersion:           "",
						GatewayIP:            "10.100.0.1",
						SubnetCIDR:           "",
						Bridge:               "",
						CPU:                  "",
						Memory:               "",
						DiskSize:             "",
						CNI:                  "cilium",
						ContainerRuntime:     "",
						InstallMetalLB:       false, // Boolean - should always override
						InstallCloudProvider: true,  // Boolean - should always override
						SkipMetalLB:          true,  // Boolean - should always override
					}
				})

				It("should override only non-empty/non-zero fields", func() {
					merged := MergeConfigs(base, override)

					// Fields that should be overridden
					Expect(merged.Environment).To(Equal(override.Environment))
					Expect(merged.NodeCount).To(Equal(override.NodeCount))
					Expect(merged.GatewayIP).To(Equal(override.GatewayIP))
					Expect(merged.CNI).To(Equal(override.CNI))

					// Fields that should NOT be overridden (empty/zero values)
					Expect(merged.Project).To(Equal(base.Project))
					Expect(merged.NumClusters).To(Equal(base.NumClusters))
					Expect(merged.K8sVersion).To(Equal(base.K8sVersion))
					Expect(merged.SubnetCIDR).To(Equal(base.SubnetCIDR))
					Expect(merged.Bridge).To(Equal(base.Bridge))
					Expect(merged.CPU).To(Equal(base.CPU))
					Expect(merged.Memory).To(Equal(base.Memory))
					Expect(merged.DiskSize).To(Equal(base.DiskSize))
					Expect(merged.ContainerRuntime).To(Equal(base.ContainerRuntime))

					// Boolean fields are always overridden
					Expect(merged.InstallMetalLB).To(Equal(override.InstallMetalLB))
					Expect(merged.InstallCloudProvider).To(Equal(override.InstallCloudProvider))
					Expect(merged.SkipMetalLB).To(Equal(override.SkipMetalLB))
				})
			})
		})

		Context("ConfigManager.MergeConfig", func() {
			var (
				tempDir string
				cm      *ConfigManager
			)

			BeforeEach(func() {
				tempDir = GinkgoT().TempDir()
				cm = &ConfigManager{configDir: tempDir}
			})

			It("should merge with saved config", func() {
				project := "test-project"

				// Save initial config
				savedConfig := &ProjectConfig{
					Project:              project,
					Environment:          "kind",
					NumClusters:          1,
					NodeCount:            2,
					CNI:                  "calico",
					ContainerRuntime:     "docker",
					InstallMetalLB:       true,
					InstallCloudProvider: false,
					SkipMetalLB:          false,
				}

				err := cm.SaveConfig(project, savedConfig)
				Expect(err).NotTo(HaveOccurred())

				// Create command config with some overrides
				cmdConfig := &ProjectConfig{
					Project:              project,
					Environment:          "minikube",   // Override
					NumClusters:          3,            // Override
					NodeCount:            0,            // Zero - should not override
					K8sVersion:           "v1.28.0",    // New field
					GatewayIP:            "10.100.0.1", // New field
					SubnetCIDR:           "",           // Empty - should not override
					Bridge:               "",           // Empty - should not override
					CPU:                  "8",          // New field
					Memory:               "16GiB",      // New field
					DiskSize:             "",           // Empty - should not override
					CNI:                  "cilium",     // Override
					ContainerRuntime:     "",           // Empty - should not override
					InstallMetalLB:       false,        // Boolean override
					InstallCloudProvider: true,         // Boolean override
					SkipMetalLB:          true,         // Boolean override
				}

				// Merge configs
				mergedConfig, err := cm.MergeConfig(project, cmdConfig)
				Expect(err).NotTo(HaveOccurred())
				Expect(mergedConfig).NotTo(BeNil())

				// Verify overridden fields
				Expect(mergedConfig.Environment).To(Equal(cmdConfig.Environment))
				Expect(mergedConfig.NumClusters).To(Equal(cmdConfig.NumClusters))
				Expect(mergedConfig.CNI).To(Equal(cmdConfig.CNI))

				// Verify fields that should not be overridden
				Expect(mergedConfig.NodeCount).To(Equal(savedConfig.NodeCount))
				Expect(mergedConfig.ContainerRuntime).To(Equal(savedConfig.ContainerRuntime))

				// Verify new fields
				Expect(mergedConfig.K8sVersion).To(Equal(cmdConfig.K8sVersion))
				Expect(mergedConfig.GatewayIP).To(Equal(cmdConfig.GatewayIP))
				Expect(mergedConfig.CPU).To(Equal(cmdConfig.CPU))
				Expect(mergedConfig.Memory).To(Equal(cmdConfig.Memory))

				// Verify boolean overrides
				Expect(mergedConfig.InstallMetalLB).To(Equal(cmdConfig.InstallMetalLB))
				Expect(mergedConfig.InstallCloudProvider).To(Equal(cmdConfig.InstallCloudProvider))
				Expect(mergedConfig.SkipMetalLB).To(Equal(cmdConfig.SkipMetalLB))
			})

			It("should return cmdConfig when no saved config exists", func() {
				project := "new-project"
				cmdConfig := &ProjectConfig{
					Project:              project,
					Environment:          "kind",
					NumClusters:          2,
					NodeCount:            3,
					CNI:                  "cilium",
					ContainerRuntime:     "containerd",
					InstallMetalLB:       false,
					InstallCloudProvider: true,
					SkipMetalLB:          true,
				}

				// Merge configs (no saved config exists)
				mergedConfig, err := cm.MergeConfig(project, cmdConfig)
				Expect(err).NotTo(HaveOccurred())
				Expect(mergedConfig).NotTo(BeNil())

				// Should return cmdConfig as-is since no saved config exists
				Expect(mergedConfig.Project).To(Equal(cmdConfig.Project))
				Expect(mergedConfig.Environment).To(Equal(cmdConfig.Environment))
				Expect(mergedConfig.NumClusters).To(Equal(cmdConfig.NumClusters))
				Expect(mergedConfig.NodeCount).To(Equal(cmdConfig.NodeCount))
				Expect(mergedConfig.CNI).To(Equal(cmdConfig.CNI))
				Expect(mergedConfig.ContainerRuntime).To(Equal(cmdConfig.ContainerRuntime))
				Expect(mergedConfig.InstallMetalLB).To(Equal(cmdConfig.InstallMetalLB))
				Expect(mergedConfig.InstallCloudProvider).To(Equal(cmdConfig.InstallCloudProvider))
				Expect(mergedConfig.SkipMetalLB).To(Equal(cmdConfig.SkipMetalLB))
			})
		})
	})
})
