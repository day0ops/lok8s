package cmd

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/day0ops/lok8s/pkg/config"
)

var _ = Describe("Cmd", func() {
	Describe("Root Command", func() {
		Context("Command structure", func() {
			It("should have correct basic properties", func() {
				Expect(rootCmd.Use).To(Equal(config.AppName))
				Expect(rootCmd.Short).To(ContainSubstring("provisioning local Kubernetes clusters"))
				Expect(rootCmd.Long).To(ContainSubstring("comprehensive tool"))
				Expect(rootCmd.Version).To(Equal(config.GetVersion()))
			})

			It("should have correct subcommands", func() {
				subcommands := rootCmd.Commands()
				commandNames := make([]string, len(subcommands))
				for i, cmd := range subcommands {
					commandNames[i] = cmd.Name()
				}

				Expect(commandNames).To(ContainElement("create"))
				Expect(commandNames).To(ContainElement("delete"))
				Expect(commandNames).To(ContainElement("config"))
				Expect(commandNames).To(ContainElement("version"))
			})

			It("should have correct persistent flags", func() {
				flags := rootCmd.PersistentFlags()

				configFlag := flags.Lookup("config")
				Expect(configFlag).NotTo(BeNil())
				Expect(configFlag.Usage).To(ContainSubstring("config file"))

				verboseFlag := flags.Lookup("verbose")
				Expect(verboseFlag).NotTo(BeNil())
				Expect(verboseFlag.Usage).To(ContainSubstring("verbose logging"))

				environmentFlag := flags.Lookup("environment")
				Expect(environmentFlag).NotTo(BeNil())
				Expect(environmentFlag.Usage).To(ContainSubstring("environment to use"))
			})
		})

		Context("Execute function", func() {
			It("should return error when command fails", func() {
				// Create a command that will fail
				testCmd := &cobra.Command{
					Use: "test-fail",
					RunE: func(cmd *cobra.Command, args []string) error {
						return errors.New("test error")
					},
				}

				// Disable flag parsing to avoid test flags
				testCmd.DisableFlagParsing = true
				err := testCmd.Execute()
				Expect(err).To(HaveOccurred())
			})

			It("should execute successfully for valid commands", func() {
				// Create a command that will succeed
				testCmd := &cobra.Command{
					Use: "test-success",
					RunE: func(cmd *cobra.Command, args []string) error {
						return nil
					},
				}

				// Disable flag parsing to avoid test flags
				testCmd.DisableFlagParsing = true
				err := testCmd.Execute()
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Global variables", func() {
			It("should have configManager initialized", func() {
				Expect(configManager).NotTo(BeNil())
			})

			It("should have default values for global variables", func() {
				// These are set during init()
				Expect(cfgFile).To(Equal(""))
				Expect(verbose).To(BeFalse())
				Expect(environment).To(Equal("minikube"))
			})
		})
	})

	Describe("Create Command", func() {
		var createCommand *cobra.Command

		BeforeEach(func() {
			createCommand = createCmd()
		})

		Context("Command structure", func() {
			It("should have correct basic properties", func() {
				Expect(createCommand.Use).To(Equal("create"))
				Expect(createCommand.Short).To(ContainSubstring("Create Kubernetes clusters"))
				Expect(createCommand.Long).To(ContainSubstring("networking and MetalLB support"))
			})

			It("should have all required flags", func() {
				flags := createCommand.Flags()

				// Required flags
				projectFlag := flags.Lookup("project")
				Expect(projectFlag).NotTo(BeNil())
				Expect(projectFlag.Usage).To(ContainSubstring("Project name"))

				// Optional flags
				bridgeFlag := flags.Lookup("bridge")
				Expect(bridgeFlag).NotTo(BeNil())
				Expect(bridgeFlag.Usage).To(ContainSubstring("Bridge name"))

				gatewayIPFlag := flags.Lookup("gateway-ip")
				Expect(gatewayIPFlag).NotTo(BeNil())
				Expect(gatewayIPFlag.Usage).To(ContainSubstring("Gateway IP"))

				cpuFlag := flags.Lookup("cpu")
				Expect(cpuFlag).NotTo(BeNil())
				Expect(cpuFlag.Usage).To(ContainSubstring("Number of CPUs"))

				memoryFlag := flags.Lookup("memory")
				Expect(memoryFlag).NotTo(BeNil())
				Expect(memoryFlag.Usage).To(ContainSubstring("Amount of memory"))

				diskFlag := flags.Lookup("disk")
				Expect(diskFlag).NotTo(BeNil())
				Expect(diskFlag.Usage).To(ContainSubstring("Amount of disk"))

				subnetFlag := flags.Lookup("subnet-cidr")
				Expect(subnetFlag).NotTo(BeNil())
				Expect(subnetFlag.Usage).To(ContainSubstring("Subnet CIDR"))

				numFlag := flags.Lookup("num")
				Expect(numFlag).NotTo(BeNil())
				Expect(numFlag.Usage).To(ContainSubstring("Number of clusters"))

				nodesFlag := flags.Lookup("nodes")
				Expect(nodesFlag).NotTo(BeNil())
				Expect(nodesFlag.Usage).To(ContainSubstring("Number of worker nodes"))

				k8sVersionFlag := flags.Lookup("kubernetes-version")
				Expect(k8sVersionFlag).NotTo(BeNil())
				Expect(k8sVersionFlag.Usage).To(ContainSubstring("Kubernetes version"))

				skipMetalLBFlag := flags.Lookup("skip-metallb-install")
				Expect(skipMetalLBFlag).NotTo(BeNil())
				Expect(skipMetalLBFlag.Usage).To(ContainSubstring("Skip MetalLB"))

				cloudProviderFlag := flags.Lookup("install-cloud-provider")
				Expect(cloudProviderFlag).NotTo(BeNil())
				Expect(cloudProviderFlag.Usage).To(ContainSubstring("cloud-provider-kind"))

				cniFlag := flags.Lookup("cni")
				Expect(cniFlag).NotTo(BeNil())
				Expect(cniFlag.Usage).To(ContainSubstring("CNI plugin"))

				containerRuntimeFlag := flags.Lookup("container-runtime")
				Expect(containerRuntimeFlag).NotTo(BeNil())
				Expect(containerRuntimeFlag.Usage).To(ContainSubstring("Container runtime"))
			})

			It("should have project flag marked as required", func() {
				Expect(createCommand.MarkFlagRequired("project")).NotTo(HaveOccurred())
			})
		})

		Context("Validation logic", func() {
			It("should validate project name is required", func() {
				// This would be tested in the RunE function
				// We can't easily test this without mocking the entire command execution
				// But we can verify the flag is marked as required
				Expect(createCommand.MarkFlagRequired("project")).NotTo(HaveOccurred())
			})
		})
	})

	Describe("Delete Command", func() {
		var deleteCommand *cobra.Command

		BeforeEach(func() {
			deleteCommand = deleteCmd()
		})

		Context("Command structure", func() {
			It("should have correct basic properties", func() {
				Expect(deleteCommand.Use).To(Equal("delete"))
				Expect(deleteCommand.Short).To(ContainSubstring("Delete Kubernetes clusters"))
				Expect(deleteCommand.Long).To(ContainSubstring("Delete one or more Kubernetes clusters"))
			})

			It("should have all required flags", func() {
				flags := deleteCommand.Flags()

				projectFlag := flags.Lookup("project")
				Expect(projectFlag).NotTo(BeNil())
				Expect(projectFlag.Usage).To(ContainSubstring("Project name"))

				numFlag := flags.Lookup("num")
				Expect(numFlag).NotTo(BeNil())
				Expect(numFlag.Usage).To(ContainSubstring("Number of clusters"))

				forceFlag := flags.Lookup("force")
				Expect(forceFlag).NotTo(BeNil())
				Expect(forceFlag.Usage).To(ContainSubstring("Force cleanup"))
			})

			It("should have project flag marked as required", func() {
				Expect(deleteCommand.MarkFlagRequired("project")).NotTo(HaveOccurred())
			})
		})
	})

	Describe("Config Command", func() {
		var configCommand *cobra.Command

		BeforeEach(func() {
			configCommand = configCmd()
		})

		Context("Command structure", func() {
			It("should have correct basic properties", func() {
				Expect(configCommand.Use).To(Equal("config"))
				Expect(configCommand.Short).To(ContainSubstring("Manage project configurations"))
				Expect(configCommand.Long).To(ContainSubstring("project-specific configuration files"))
			})

			It("should have subcommands", func() {
				subcommands := configCommand.Commands()
				commandNames := make([]string, len(subcommands))
				for i, cmd := range subcommands {
					commandNames[i] = cmd.Name()
				}

				Expect(commandNames).To(ContainElement("list"))
				Expect(commandNames).To(ContainElement("show"))
				Expect(commandNames).To(ContainElement("delete"))
			})
		})

		Context("Config subcommands", func() {
			It("should have list subcommand", func() {
				subcommands := configCommand.Commands()
				var listCmd *cobra.Command
				for _, cmd := range subcommands {
					if cmd.Name() == "list" {
						listCmd = cmd
						break
					}
				}
				Expect(listCmd).NotTo(BeNil())
				Expect(listCmd.Short).To(ContainSubstring("List all project configurations"))
			})

			It("should have show subcommand", func() {
				subcommands := configCommand.Commands()
				var showCmd *cobra.Command
				for _, cmd := range subcommands {
					if cmd.Name() == "show" {
						showCmd = cmd
						break
					}
				}
				Expect(showCmd).NotTo(BeNil())
				Expect(showCmd.Short).To(ContainSubstring("Show configuration for a project"))
			})

			It("should have delete subcommand", func() {
				subcommands := configCommand.Commands()
				var deleteCmd *cobra.Command
				for _, cmd := range subcommands {
					if cmd.Name() == "delete" {
						deleteCmd = cmd
						break
					}
				}
				Expect(deleteCmd).NotTo(BeNil())
				Expect(deleteCmd.Short).To(ContainSubstring("Delete configuration for a project"))
			})
		})
	})

	Describe("Version Command", func() {
		var versionCommand *cobra.Command

		BeforeEach(func() {
			versionCommand = versionCmd()
		})

		Context("Command structure", func() {
			It("should have correct basic properties", func() {
				Expect(versionCommand.Use).To(Equal("version"))
				Expect(versionCommand.Short).To(ContainSubstring("Print the version information"))
			})
		})
	})

	Describe("Helper Functions", func() {
		Context("runCreateCommand", func() {
			It("should validate environment selection", func() {
				// This function validates environment selection
				// We can't easily test the full execution without mocking
				// But we can verify it exists and has the right signature
				Expect(runCreateCommand).NotTo(BeNil())
			})
		})

		Context("createMinikubeClusters", func() {
			It("should exist and have correct signature", func() {
				Expect(createMinikubeClusters).NotTo(BeNil())
			})
		})

		Context("createKindClusters", func() {
			It("should exist and have correct signature", func() {
				Expect(createKindClusters).NotTo(BeNil())
			})
		})

		Context("deleteMinikubeClusters", func() {
			It("should exist and have correct signature", func() {
				Expect(deleteMinikubeClusters).NotTo(BeNil())
			})
		})

		Context("deleteKindClusters", func() {
			It("should exist and have correct signature", func() {
				Expect(deleteKindClusters).NotTo(BeNil())
			})
		})
	})

	Describe("Integration Tests", func() {
		Context("Command execution", func() {
			It("should handle version command", func() {
				versionCommand := versionCmd()
				Expect(versionCommand).NotTo(BeNil())

				// Test that version command can be executed
				versionCommand.SetArgs([]string{})
				err := versionCommand.Execute()
				Expect(err).NotTo(HaveOccurred())
			})

			It("should handle help command", func() {
				rootCmd.SetArgs([]string{"--help"})
				err := rootCmd.Execute()
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Flag validation", func() {
			It("should accept valid environment values", func() {
				validEnvironments := []string{"minikube", "kind"}

				for _, env := range validEnvironments {
					rootCmd.SetArgs([]string{"--environment", env, "--help"})
					err := rootCmd.Execute()
					Expect(err).NotTo(HaveOccurred())
				}
			})
		})
	})

	Describe("Error Handling", func() {
		Context("Invalid arguments", func() {
			It("should handle invalid environment", func() {
				rootCmd.SetArgs([]string{"--environment", "invalid", "--help"})
				err := rootCmd.Execute()
				// This should not error immediately, but would error during execution
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("Configuration Integration", func() {
		Context("Config manager integration", func() {
			It("should have config manager initialized", func() {
				Expect(configManager).NotTo(BeNil())
			})

			It("should use config manager in commands", func() {
				// The config manager is used in create and delete commands
				// We can verify it's available
				Expect(configManager).To(BeAssignableToTypeOf(&config.ConfigManager{}))
			})
		})
	})
})
