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

package cmd

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/day0ops/lok8s/pkg/util/docker"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/day0ops/lok8s/pkg/cluster/kind"
	"github.com/day0ops/lok8s/pkg/cluster/minikube"
	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
)

var (
	cfgFile       string
	verbose       bool
	environment   string
	configManager *config.ConfigManager
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   config.AppName,
	Short: "A tool for provisioning local Kubernetes clusters (multi) with network management",
	Long: strings.Replace(`[config.AppName] is a comprehensive tool that combines cluster provisioning 
with advanced network management capabilities. It supports both Kind and Minikube clusters
on Linux and macOS platforms.

Default behavior: If no --environment flag is specified, [config.AppName] will default to minikube.
Use '[config.AppName] --environment kind' to use kind instead.`, "[config.AppName]", config.AppName, -1),
	Version: config.GetVersion(),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initializeConfig()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// default behavior: run create command with the specified environment
		return runCreateCommand(cmd, args)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// initialize config manager
	configManager = config.NewConfigManager()

	// global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (YAML format, can be located anywhere)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose logging")
	rootCmd.PersistentFlags().StringVarP(&environment, "environment", "e", "minikube", "environment to use (minikube or kind)")

	// add subcommands
	rootCmd.AddCommand(createCmd())
	rootCmd.AddCommand(deleteCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(profileListCmd())
	rootCmd.AddCommand(imageLoadCmd())
	rootCmd.AddCommand(configCmd())
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(kindTunnelCmd())
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// search config in home directory with name ".<appname>" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName("." + config.AppName)
	}

	viper.AutomaticEnv() // read in environment variables that match

	// if a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		logrus.Debug("Using config file:", viper.ConfigFileUsed())
	}
}

func initializeConfig() error {
	// initialize logger
	if verbose {
		logger.SetLevel(logrus.DebugLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	return nil
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(config.AppName+" version %s\n", config.GetVersion())
		},
	}
}

// createCmd creates clusters using the specified environment
func createCmd() *cobra.Command {
	var (
		project              string
		bridge               string
		gatewayIP            string
		cpu                  string
		memory               string
		disk                 string
		subnetCIDR           string
		numClusters          int
		nodeCount            int
		k8sVersion           string
		skipMetalLB          bool
		installCloudProvider bool
		cni                  string
		containerRuntime     string
		containerEngine      string
		recreate             bool
	)

	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create Kubernetes clusters",
		Long:         `Create one or more Kubernetes clusters with networking and MetalLB support`,
		SilenceUsage: true, // dont display usage for errors
		RunE: func(cmd *cobra.Command, args []string) error {
			// check if running as sudo/root
			if syscall.Geteuid() == 0 {
				return fmt.Errorf("create command must not be run as sudo/root")
			}

			if project == "" {
				return fmt.Errorf("project name is required")
			}

			// create command config from flags
			cmdConfig := &config.ProjectConfig{
				Project:              project,
				Environment:          environment,
				NumClusters:          numClusters,
				NodeCount:            nodeCount,
				K8sVersion:           k8sVersion,
				GatewayIP:            gatewayIP,
				SubnetCIDR:           subnetCIDR,
				Bridge:               bridge,
				CPU:                  cpu,
				Memory:               memory,
				DiskSize:             disk,
				CNI:                  cni,
				ContainerRuntime:     containerRuntime,
				ContainerEngine:      containerEngine,
				InstallMetalLB:       !skipMetalLB,
				InstallCloudProvider: installCloudProvider,
				SkipMetalLB:          skipMetalLB,
			}

			// load user-defined config file if specified
			if cfgFile != "" {
				userConfig, err := config.LoadConfigFromFile(cfgFile)
				if err != nil {
					return fmt.Errorf("failed to load config file %s: %w", cfgFile, err)
				}
				logger.Infof("loaded configuration from file: %s", cfgFile)

				// merge user config with command line config
				cmdConfig = config.MergeConfigs(userConfig, cmdConfig)
			}

			// load and merge with saved config (for persistence)
			finalConfig, err := configManager.MergeConfig(project, cmdConfig)
			if err != nil {
				return fmt.Errorf("failed to load project config: %w", err)
			}

			// auto determine the container engine if one isn't determined
			if finalConfig.Environment == "kind" && finalConfig.ContainerEngine == "" {
				engine, err := docker.GetContainerRuntime()
				if err != nil {
					return fmt.Errorf("failed to get container runtime: %w", err)
				}
				finalConfig.ContainerEngine = engine
			}

			// validate merged config
			if finalConfig.NumClusters < 1 || finalConfig.NumClusters > 3 {
				return fmt.Errorf("number of clusters must be between 1 and 3")
			}

			// validate container runtime
			validRuntimes := []string{"containerd", "cri-o", "docker"}
			isValidRuntime := false
			for _, runtime := range validRuntimes {
				if finalConfig.ContainerRuntime == runtime {
					isValidRuntime = true
					break
				}
			}
			if !isValidRuntime {
				return fmt.Errorf("invalid container runtime: %s. Valid options are: %s", finalConfig.ContainerRuntime, strings.Join(validRuntimes, ", "))
			}

			// validate CNI
			validCNIs := []string{"calico", "cilium", "flannel", "kindnet"}
			isValidCNI := false
			for _, cniOption := range validCNIs {
				if finalConfig.CNI == cniOption {
					isValidCNI = true
					break
				}
			}
			if !isValidCNI {
				return fmt.Errorf("invalid CNI: %s. Valid options are: %s", finalConfig.CNI, strings.Join(validCNIs, ", "))
			}

			// validate kind container engine if specified
			if finalConfig.Environment == "kind" && finalConfig.ContainerEngine != "" {
				validKindEngines := []string{"docker", "podman"}
				isValidKindEngine := false
				for _, engine := range validKindEngines {
					if finalConfig.ContainerEngine == engine {
						isValidKindEngine = true
						break
					}
				}
				if !isValidKindEngine {
					return fmt.Errorf("invalid container engine: %s. Valid options are: %s", finalConfig.ContainerEngine, strings.Join(validKindEngines, ", "))
				}
			}

			if finalConfig.Environment == "minikube" {
				return createMinikubeClusters(finalConfig, configManager)
			} else if finalConfig.Environment == "kind" {
				return createKindClusters(finalConfig, recreate, configManager)
			}
			return fmt.Errorf("invalid environment: %s", finalConfig.Environment)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Project name (required)")
	cmd.Flags().StringVarP(&bridge, "bridge", "b", config.MinikubeDefaultBridgeNetName, "Bridge name (Minikube on Linux only)")
	cmd.Flags().StringVarP(&gatewayIP, "gateway-ip", "g", config.KindNetworkGatewayIP, "Gateway IP address (Kind only). If not specified will automatically determine from the given network subnet")
	cmd.Flags().StringVarP(&cpu, "cpu", "c", config.MinikubeCPU, "Number of CPUs to allocate (Minikube only)")
	cmd.Flags().StringVarP(&memory, "memory", "m", config.MinikubeMemory, "Amount of memory to allocate (Minikube only)")
	cmd.Flags().StringVarP(&disk, "disk", "d", config.MinikubeDiskSize, "Amount of disk space to allocate (Minikube only)")
	cmd.Flags().StringVarP(&subnetCIDR, "subnet-cidr", "s", config.DefaultNetworkSubnetCIDR, "Subnet CIDR for the network (Linux & Minikube only)")
	cmd.Flags().IntVarP(&numClusters, "num", "n", config.DefaultClusterNum, "Number of clusters to create (1-3)")
	cmd.Flags().IntVarP(&nodeCount, "nodes", "z", config.DefaultNodeCount, "Number of worker nodes per cluster")
	cmd.Flags().StringVarP(&k8sVersion, "kubernetes-version", "k", "stable", "Kubernetes version to use")
	cmd.Flags().BoolVar(&skipMetalLB, "skip-metallb-install", false, "Skip MetalLB load balancer installation")
	cmd.Flags().BoolVar(&installCloudProvider, "install-cloud-provider", false, "Install cloud-provider-kind for load balancer functionality (Kind only, preferred over MetalLB)")
	cmd.Flags().StringVar(&cni, "cni", "cilium", "CNI plugin to use (Options: calico, cilium, flannel, or kindnet)")
	cmd.Flags().StringVar(&containerRuntime, "container-runtime", "containerd", "Container runtime to use (Kind only, Options: containerd, cri-o, or docker)")
	cmd.Flags().StringVar(&containerEngine, "container-engine", "", "Preferred container engine for kind clusters (Kind only, Options: docker or podman). If not specified, auto-detects available engine")
	cmd.Flags().BoolVar(&recreate, "recreate", false, "Recreate clusters even if they already exist (will delete existing clusters first)")

	if err := cmd.MarkFlagRequired("project"); err != nil {
		logger.Warnf("failed to mark project flag as required: %v", err)
	}

	return cmd
}

// deleteCmd deletes clusters using the specified environment
func deleteCmd() *cobra.Command {
	var (
		project     string
		numClusters int
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete Kubernetes clusters",
		Long:  `Delete one or more Kubernetes clusters`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// check if running as sudo/root
			if syscall.Geteuid() == 0 {
				return fmt.Errorf("delete command must not be run as sudo/root")
			}

			if project == "" {
				return fmt.Errorf("project name is required")
			}

			// load saved config to get environment and other settings
			savedConfig, err := configManager.LoadConfig(project)
			if err != nil {
				return fmt.Errorf("failed to load project config: %w", err)
			}

			// use saved config if available, otherwise use defaults
			env := environment
			clusters := numClusters
			if savedConfig != nil {
				if savedConfig.Environment != "" {
					env = savedConfig.Environment
				}
				if savedConfig.NumClusters > 0 {
					clusters = savedConfig.NumClusters
				}
			}

			if clusters < 1 || clusters > 3 {
				return fmt.Errorf("number of clusters must be between 1 and 3")
			}

			if env == "minikube" {
				return deleteMinikubeClusters(project, clusters, force)
			} else if env == "kind" {
				return deleteKindClusters(project, clusters, force)
			}
			return fmt.Errorf("invalid environment: %s", env)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Project name (required)")
	cmd.Flags().IntVarP(&numClusters, "num", "n", 1, "Number of clusters to delete (1-3)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force cleanup")

	if err := cmd.MarkFlagRequired("project"); err != nil {
		logger.Warnf("failed to mark project flag as required: %v", err)
	}

	return cmd
}

// runCreateCommand handles the create command with environment selection
func runCreateCommand(cmd *cobra.Command, args []string) error {
	// validate environment selection
	if environment != "minikube" && environment != "kind" {
		return fmt.Errorf("invalid environment '%s'. Must be 'minikube' or 'kind'", environment)
	}

	// show help for create command
	fmt.Printf("Creating clusters using %s environment.\n", environment)
	fmt.Println("Use '" + config.AppName + " create --help' for create command options.")
	fmt.Println("Use '" + config.AppName + " --environment kind' to use kind instead.")
	fmt.Println()

	createCmd := createCmd()
	createCmd.SetArgs([]string{"--help"})
	return createCmd.Execute()
}

// Helper functions to call the appropriate managers
func createMinikubeClusters(finalConfig *config.ProjectConfig, configManager *config.ConfigManager) error {
	opts := &minikube.CreateOptions{
		Project:          finalConfig.Project,
		Bridge:           finalConfig.Bridge,
		CPU:              finalConfig.CPU,
		Memory:           finalConfig.Memory,
		Disk:             finalConfig.DiskSize,
		SubnetCIDR:       finalConfig.SubnetCIDR,
		NumClusters:      finalConfig.NumClusters,
		NodeCount:        finalConfig.NodeCount,
		K8sVersion:       finalConfig.K8sVersion,
		InstallMetalLB:   finalConfig.InstallMetalLB,
		Verbose:          verbose,
		CNI:              finalConfig.CNI,
		ContainerRuntime: finalConfig.ContainerRuntime,
	}

	manager := minikube.NewManager()
	err := manager.CreateClusters(opts)
	if err != nil {
		return err
	}

	// Update finalConfig with actual subnet used (may have been changed by FreeSubnet)
	if opts.SubnetCIDR != "" && opts.SubnetCIDR != finalConfig.SubnetCIDR {
		finalConfig.SubnetCIDR = opts.SubnetCIDR
		logger.Debugf("updating saved config with actual subnet: %s", finalConfig.SubnetCIDR)
	}

	// save config only after successful cluster creation
	if err := configManager.SaveConfig(finalConfig.Project, finalConfig); err != nil {
		logger.Warnf("failed to save project config: %v", err)
	}

	return nil
}

func createKindClusters(finalConfig *config.ProjectConfig, recreate bool, configManager *config.ConfigManager) error {
	opts := &kind.CreateOptions{
		Project:                  finalConfig.Project,
		GatewayIP:                finalConfig.GatewayIP,
		SubnetCIDR:               finalConfig.SubnetCIDR,
		NumClusters:              finalConfig.NumClusters,
		NodeCount:                finalConfig.NodeCount,
		K8sVersion:               finalConfig.K8sVersion,
		InstallMetalLB:           finalConfig.InstallMetalLB,
		InstallCloudProvider:     finalConfig.InstallCloudProvider,
		CNI:                      finalConfig.CNI,
		ContainerRuntime:         finalConfig.ContainerRuntime,
		PreferredContainerEngine: finalConfig.ContainerEngine,
		Recreate:                 recreate,
	}

	manager := kind.NewManager()
	err := manager.CreateClusters(opts)
	if err != nil {
		return err
	}

	// save config only after successful cluster creation
	if err := configManager.SaveConfig(finalConfig.Project, finalConfig); err != nil {
		logger.Warnf("failed to save project config: %v", err)
	}

	return nil
}

func deleteMinikubeClusters(project string, numClusters int, force bool) error {
	// load saved config to get Bridge and SubnetCIDR
	savedConfig, err := configManager.LoadConfig(project)
	if err != nil {
		logger.Warnf("failed to load saved config for project %s: %v", project, err)
	}

	// use saved config values if available, otherwise use defaults
	bridge := config.MinikubeDefaultBridgeNetName
	subnetCIDR := config.DefaultNetworkSubnetCIDR
	if savedConfig != nil {
		if savedConfig.Bridge != "" {
			bridge = savedConfig.Bridge
		}
		if savedConfig.SubnetCIDR != "" {
			subnetCIDR = savedConfig.SubnetCIDR
		}
	}

	opts := &minikube.DeleteOptions{
		Project:     project,
		NumClusters: numClusters,
		Force:       force,
		Bridge:      bridge,
		SubnetCIDR:  subnetCIDR,
	}

	manager := minikube.NewManager()
	return manager.DeleteClusters(opts)
}

func deleteKindClusters(project string, numClusters int, force bool) error {
	opts := &kind.DeleteOptions{
		Project:     project,
		NumClusters: numClusters,
		Force:       force,
	}

	manager := kind.NewManager()
	return manager.DeleteClusters(opts)
}

// statusCmd shows the status of clusters
func statusCmd() *cobra.Command {
	var (
		project string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of Kubernetes clusters",
		Long:  `Show the status of one or more Kubernetes clusters for a project`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return fmt.Errorf("project name is required")
			}

			// load saved config to get environment and other settings
			savedConfig, err := configManager.LoadConfig(project)
			if err != nil {
				return fmt.Errorf("failed to load project config: %w", err)
			}

			// use saved config if available, otherwise use defaults
			env := environment
			clusters := 1
			if savedConfig != nil {
				if savedConfig.Environment != "" {
					env = savedConfig.Environment
				}
				if savedConfig.NumClusters > 0 {
					clusters = savedConfig.NumClusters
				}
			}

			if clusters < 1 || clusters > 3 {
				return fmt.Errorf("number of clusters must be between 1 and 3")
			}

			if env == "minikube" {
				return statusMinikubeClusters(project, clusters)
			} else if env == "kind" {
				return statusKindClusters(project, clusters)
			}
			return fmt.Errorf("invalid environment: %s", env)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Project name (required)")

	if err := cmd.MarkFlagRequired("project"); err != nil {
		logger.Warnf("failed to mark project flag as required: %v", err)
	}

	return cmd
}

func statusMinikubeClusters(project string, numClusters int) error {
	opts := &minikube.StatusOptions{
		Project:     project,
		NumClusters: numClusters,
	}

	manager := minikube.NewManager()
	return manager.StatusClusters(opts)
}

func statusKindClusters(project string, numClusters int) error {
	opts := &kind.StatusOptions{
		Project:     project,
		NumClusters: numClusters,
	}

	manager := kind.NewManager()
	return manager.StatusClusters(opts)
}

// profileListCmd lists profiles/clusters
func profileListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile-list",
		Short: "List all profiles/clusters",
		Long:  `List all profiles for Minikube or clusters for Kind`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "minikube" {
				return listMinikubeProfiles()
			} else if environment == "kind" {
				return listKindClusters()
			}
			return fmt.Errorf("invalid environment: %s", environment)
		},
	}

	return cmd
}

func listMinikubeProfiles() error {
	manager := minikube.NewManager()
	return manager.ListProfiles()
}

func listKindClusters() error {
	manager := kind.NewManager()
	return manager.ListClusters()
}

// imageLoadCmd loads Docker images into clusters
func imageLoadCmd() *cobra.Command {
	var (
		project string
		image   string
	)

	cmd := &cobra.Command{
		Use:   "image-load",
		Short: "Load Docker images into clusters",
		Long:  `Load a Docker image into all clusters for a project`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return fmt.Errorf("project name is required")
			}

			if image == "" {
				return fmt.Errorf("image name is required")
			}

			// load saved config to get environment and number of clusters
			savedConfig, err := configManager.LoadConfig(project)
			if err != nil {
				return fmt.Errorf("failed to load project config: %w", err)
			}

			// use saved config if available, otherwise use defaults
			env := environment
			clusters := 1
			if savedConfig != nil {
				if savedConfig.Environment != "" {
					env = savedConfig.Environment
				}
				if savedConfig.NumClusters > 0 {
					clusters = savedConfig.NumClusters
				}
			}

			if clusters < 1 || clusters > 3 {
				return fmt.Errorf("number of clusters must be between 1 and 3")
			}

			if env == "minikube" {
				return loadImageMinikube(project, image, clusters)
			} else if env == "kind" {
				return loadImageKind(project, image, clusters)
			}
			return fmt.Errorf("invalid environment: %s", env)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Project name (required)")
	cmd.Flags().StringVarP(&image, "image", "i", "", "Docker image name to load (required)")

	if err := cmd.MarkFlagRequired("project"); err != nil {
		logger.Warnf("failed to mark project flag as required: %v", err)
	}
	if err := cmd.MarkFlagRequired("image"); err != nil {
		logger.Warnf("failed to mark image flag as required: %v", err)
	}

	return cmd
}

func loadImageMinikube(project, image string, numClusters int) error {
	opts := &minikube.LoadImageOptions{
		Project:     project,
		Image:       image,
		NumClusters: numClusters,
	}

	manager := minikube.NewManager()
	return manager.LoadImage(opts)
}

func loadImageKind(project, image string, numClusters int) error {
	opts := &kind.LoadImageOptions{
		Project:     project,
		Image:       image,
		NumClusters: numClusters,
	}

	manager := kind.NewManager()
	return manager.LoadImage(opts)
}

// configCmd manages project configurations
func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage project configurations",
		Long:  `Manage project-specific configuration files stored in $HOME/.lok8/`,
	}

	// list command
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all project configurations",
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := configManager.ListConfigs()
			if err != nil {
				return fmt.Errorf("failed to list configs: %w", err)
			}

			if len(projects) == 0 {
				fmt.Println("No project configurations found.")
				return nil
			}

			fmt.Println("Project configurations:")
			for _, project := range projects {
				fmt.Printf("  - %s\n", project)
			}
			return nil
		},
	}

	// show command
	showCmd := &cobra.Command{
		Use:   "show [project]",
		Short: "Show configuration for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := args[0]
			projectConfig, err := configManager.LoadConfig(project)
			if err != nil {
				return fmt.Errorf("failed to load config for project %s: %w", project, err)
			}

			if projectConfig == nil {
				fmt.Printf("No configuration found for project: %s\n", project)
				return nil
			}

			fmt.Printf("Configuration for project: %s\n", project)
			fmt.Printf("  Environment: %s\n", projectConfig.Environment)
			fmt.Printf("  Clusters: %d\n", projectConfig.NumClusters)
			fmt.Printf("  Nodes: %d\n", projectConfig.NodeCount)
			fmt.Printf("  Kubernetes Version: %s\n", projectConfig.K8sVersion)
			fmt.Printf("  Gateway IP: %s\n", projectConfig.GatewayIP)
			fmt.Printf("  Subnet CIDR: %s\n", projectConfig.SubnetCIDR)
			fmt.Printf("  CNI: %s\n", projectConfig.CNI)
			fmt.Printf("  Container Runtime: %s\n", projectConfig.ContainerRuntime)
			fmt.Printf("  Install MetalLB: %v\n", projectConfig.InstallMetalLB)
			fmt.Printf("  Install Cloud Provider: %v\n", projectConfig.InstallCloudProvider)
			return nil
		},
	}

	// delete command
	deleteCmd := &cobra.Command{
		Use:   "delete [project]",
		Short: "Delete configuration for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := args[0]
			if err := configManager.DeleteConfig(project); err != nil {
				return fmt.Errorf("failed to delete config for project %s: %w", project, err)
			}
			fmt.Printf("Deleted configuration for project: %s\n", project)
			return nil
		},
	}

	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(deleteCmd)

	return cmd
}
