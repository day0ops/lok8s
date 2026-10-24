package native

import (
	"github.com/day0ops/lok8s/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("parseLimactlVersion", func() {
	It("parses the version from `limactl --version` output", func() {
		version, err := parseLimactlVersion([]byte("limactl version 2.2.1\n"))

		Expect(err).NotTo(HaveOccurred())
		Expect(version).To(Equal("2.2.1"))
	})

	It("errors when the output doesn't contain a version", func() {
		_, err := parseLimactlVersion([]byte("garbage output"))

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("buildLimaYAML", func() {
	It("includes both architectures' pinned images, sized resources, and the named network", func() {
		content, err := buildLimaYAML("lok8s-testproj")

		Expect(err).NotTo(HaveOccurred())

		var parsed limaTemplateSpec
		Expect(yaml.Unmarshal([]byte(content), &parsed)).To(Succeed())

		Expect(parsed.Images).To(ConsistOf(
			limaImageSpec{Location: config.NativeLimaBaseImageAMD64, Arch: "x86_64"},
			limaImageSpec{Location: config.NativeLimaBaseImageARM64, Arch: "aarch64"},
		))
		Expect(parsed.CPUs).To(Equal(2))
		Expect(parsed.Memory).To(Equal(config.NativeLimaMemory))
		Expect(parsed.Networks).To(ConsistOf(
			limaNetworkSpec{Lima: "lok8s-testproj", Interface: config.NativeLimaNetworkInterface},
		))

		Expect(parsed.Containerd.System).NotTo(BeNil())
		Expect(*parsed.Containerd.System).To(BeFalse())
		Expect(parsed.Containerd.User).NotTo(BeNil())
		Expect(*parsed.Containerd.User).To(BeFalse())
	})
})

var _ = Describe("parseIPv4FromAddrShow", func() {
	It("extracts the address from `ip -4 -o addr show` output", func() {
		output := `3: lima0    inet 192.168.105.5/24 brd 192.168.105.255 scope global lima0\       valid_lft forever preferred_lft forever`

		ip, err := parseIPv4FromAddrShow(output)

		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("192.168.105.5"))
	})

	It("errors when no address is present", func() {
		_, err := parseIPv4FromAddrShow("")

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("parseLimaNetworkListJSON", func() {
	It("parses one JSON object per line, not a JSON array", func() {
		output := []byte("{\"name\":\"lok8s-proj\",\"mode\":\"shared\",\"gateway\":\"10.89.0.1\",\"netmask\":\"255.255.0.0\"}\n{\"name\":\"bridged\",\"mode\":\"bridged\",\"interface\":\"en0\"}\n")

		entries, err := parseLimaNetworkListJSON(output)

		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(entries[0]).To(Equal(limaNetworkEntry{Name: "lok8s-proj", Mode: "shared", Gateway: "10.89.0.1", NetMask: "255.255.0.0"}))
		Expect(entries[1].Name).To(Equal("bridged"))
	})
})

var _ = Describe("subnetsFromNetworkEntries", func() {
	It("converts gateway/netmask pairs to CIDRs and skips bridged entries", func() {
		entries := []limaNetworkEntry{
			{Name: "lok8s-proj", Mode: "shared", Gateway: "10.89.0.1", NetMask: "255.255.0.0"},
			{Name: "bridged", Mode: "bridged"},
		}

		subnets := subnetsFromNetworkEntries(entries)

		Expect(subnets).To(Equal(map[string]string{"lok8s-proj": "10.89.0.0/16"}))
	})
})

var _ = Describe("parseLimaInstanceListJSON", func() {
	It("parses one JSON object per line, not a JSON array", func() {
		output := []byte("{\"name\":\"proj-control-plane\",\"status\":\"Running\"}\n{\"name\":\"proj-worker-1\",\"status\":\"Stopped\"}\n")

		instances, err := parseLimaInstanceListJSON(output)

		Expect(err).NotTo(HaveOccurred())
		Expect(instances).To(Equal([]limaInstance{
			{Name: "proj-control-plane", Status: "Running"},
			{Name: "proj-worker-1", Status: "Stopped"},
		}))
	})
})

var _ = Describe("findSHA256ForAsset", func() {
	It("finds the matching checksum line", func() {
		sums := []byte("aaaa1111  socket_vmnet-1.2.2-arm64.tar.gz\nbbbb2222  socket_vmnet-1.2.2-x86_64.tar.gz\n")

		hash, err := findSHA256ForAsset(sums, "socket_vmnet-1.2.2-arm64.tar.gz")

		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("aaaa1111"))
	})

	It("errors when the asset isn't listed", func() {
		_, err := findSHA256ForAsset([]byte("aaaa1111  other-file.tar.gz\n"), "missing.tar.gz")

		Expect(err).To(HaveOccurred())
	})
})
