package config

import (
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config", func() {
	Describe("Constants", func() {
		Context("String constants", func() {
			It("should have correct values", func() {
				Expect(AppName).To(Equal("lok8s"))
				Expect(MinikubeDefaultBridgeNetName).To(Equal("virbr50"))
				Expect(MinikubeQemuSystem).To(Equal("qemu:///system"))
				Expect(MinikubeLibvirtPvtNetworkName).To(Equal("minikube-net"))
				Expect(DefaultNetworkSubnetCIDR).To(Equal("10.89.0.0/16"))
				Expect(KindNetworkName).To(Equal("kind"))
				Expect(KindNetworkGatewayIP).To(Equal("10.89.0.1"))
				Expect(MinikubeCPU).To(Equal("4"))
				Expect(MinikubeMemory).To(Equal("8GiB"))
				Expect(MinikubeDiskSize).To(Equal("10GiB"))
				Expect(VfkitMinSupportedVersion).To(Equal("0.6.1"))
				Expect(MinikubeMinSupportedVersion).To(Equal("1.36.0"))
			})
		})

		Context("Numeric constants", func() {
			It("should have correct values", func() {
				Expect(DefaultClusterNum).To(Equal(1))
				Expect(DefaultNodeCount).To(Equal(2))
				Expect(MinikubeNetworkDHCPIPCount).To(Equal(2000))
				Expect(MetalLBRangeMinLastOctet).To(Equal(200))
				Expect(MetalLBRangeMaxLastOctet).To(Equal(254))
			})
		})

		Context("Constants validation", func() {
			It("should not be empty", func() {
				Expect(AppName).NotTo(BeEmpty())
				Expect(AppVersion).NotTo(BeEmpty())
				Expect(MinikubeDefaultBridgeNetName).NotTo(BeEmpty())
				Expect(MinikubeQemuSystem).NotTo(BeEmpty())
				Expect(MinikubeLibvirtPvtNetworkName).NotTo(BeEmpty())
				Expect(DefaultNetworkSubnetCIDR).NotTo(BeEmpty())
				Expect(KindNetworkName).NotTo(BeEmpty())
				Expect(KindNetworkGatewayIP).NotTo(BeEmpty())
				Expect(MinikubeCPU).NotTo(BeEmpty())
				Expect(MinikubeMemory).NotTo(BeEmpty())
				Expect(MinikubeDiskSize).NotTo(BeEmpty())
				Expect(VfkitMinSupportedVersion).NotTo(BeEmpty())
				Expect(MinikubeMinSupportedVersion).NotTo(BeEmpty())
			})

			It("should have positive numeric values", func() {
				Expect(DefaultClusterNum).To(BeNumerically(">", 0))
				Expect(DefaultNodeCount).To(BeNumerically(">", 0))
				Expect(MinikubeNetworkDHCPIPCount).To(BeNumerically(">", 0))
				Expect(MetalLBRangeMinLastOctet).To(BeNumerically(">", 0))
				Expect(MetalLBRangeMaxLastOctet).To(BeNumerically(">", 0))
			})
		})

		Context("MetalLB range validation", func() {
			It("should have min less than max", func() {
				Expect(MetalLBRangeMinLastOctet).To(BeNumerically("<", MetalLBRangeMaxLastOctet))
			})

			It("should be within valid IP range", func() {
				Expect(MetalLBRangeMinLastOctet).To(BeNumerically(">=", 1))
				Expect(MetalLBRangeMinLastOctet).To(BeNumerically("<=", 254))
				Expect(MetalLBRangeMaxLastOctet).To(BeNumerically(">=", 1))
				Expect(MetalLBRangeMaxLastOctet).To(BeNumerically("<=", 254))
			})
		})
	})

	Describe("Functions", func() {
		Context("Version functions", func() {
			It("should return correct version", func() {
				Expect(GetVersion()).To(Equal(AppVersion))
			})
		})

		Context("Platform detection functions", func() {
			It("should return correct OS", func() {
				Expect(GetOS()).To(Equal(runtime.GOOS))
			})

			It("should return correct architecture", func() {
				Expect(GetArch()).To(Equal(runtime.GOARCH))
			})

			It("should detect Linux correctly", func() {
				expected := runtime.GOOS == "linux"
				Expect(IsLinux()).To(Equal(expected))
			})

			It("should detect Darwin correctly", func() {
				expected := runtime.GOOS == "darwin"
				Expect(IsDarwin()).To(Equal(expected))
			})
		})

		Context("Platform detection consistency", func() {
			It("should have only one platform detection return true", func() {
				linux := IsLinux()
				darwin := IsDarwin()
				Expect(linux && darwin).To(BeFalse(), "Both IsLinux() and IsDarwin() cannot be true at the same time")
			})

			It("should have at least one platform detection return true", func() {
				linux := IsLinux()
				darwin := IsDarwin()
				Expect(linux || darwin).To(BeTrue(), "At least one platform detection function should return true")
			})
		})

		Context("Function consistency", func() {
			It("should have consistent version", func() {
				Expect(GetVersion()).To(Equal(AppVersion))
			})

			It("should have consistent OS", func() {
				Expect(GetOS()).To(Equal(runtime.GOOS))
			})

			It("should have consistent architecture", func() {
				Expect(GetArch()).To(Equal(runtime.GOARCH))
			})
		})
	})

	Describe("Data structures", func() {
		Context("Kind Kubernetes versions", func() {
			It("should contain expected versions", func() {
				expectedVersions := []string{"1.33", "1.32", "1.31", "1.30", "1.29"}
				for _, version := range expectedVersions {
					Expect(KindK8sVersions).To(HaveKey(version))
				}
			})

			It("should have valid image formats", func() {
				for version, image := range KindK8sVersions {
					Expect(image).NotTo(BeEmpty(), "KindK8sVersions[%s] should not be empty", version)
					Expect(image).To(HavePrefix("v"), "KindK8sVersions[%s] should start with 'v'", version)
				}
			})
		})

		Context("Minikube Kubernetes versions", func() {
			It("should contain expected versions", func() {
				expectedVersions := []string{"1.33", "1.32"}
				for _, version := range expectedVersions {
					Expect(MinikubeK8sVersions).To(HaveKey(version))
				}
			})

			It("should have valid version formats", func() {
				for version, k8sVersion := range MinikubeK8sVersions {
					Expect(k8sVersion).NotTo(BeEmpty(), "MinikubeK8sVersions[%s] should not be empty", version)
				}
			})
		})

		Context("Network template", func() {
			It("should not be empty", func() {
				Expect(NetworkTemplate).NotTo(BeEmpty())
			})

			It("should contain required placeholders", func() {
				expectedPlaceholders := []string{"{{.Name}}", "{{.Bridge}}", "{{.Gateway}}", "{{.Netmask}}", "{{.ClientMin}}", "{{.ClientMax}}"}
				for _, placeholder := range expectedPlaceholders {
					Expect(NetworkTemplate).To(ContainSubstring(placeholder))
				}
			})
		})
	})
})
