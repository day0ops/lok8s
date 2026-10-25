package native

import (
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewNodeClient", func() {
	It("resolves lima regardless of platform", func() {
		client, err := NewNodeClient("lima")

		Expect(err).NotTo(HaveOccurred())
		Expect(client).To(BeAssignableToTypeOf(&LimaClient{}))
	})

	It("rejects an unknown backend", func() {
		_, err := NewNodeClient("bogus")

		Expect(err).To(HaveOccurred())
	})

	It("resolves the empty backend to this platform's default without error", func() {
		// deliberately does not assert the concrete type: ContainerClient only
		// exists on a darwin build and IncusClient only on a linux build, so
		// referencing either by name here would fail to COMPILE on the other
		// platform - this file carries no //go:build tag and must compile on
		// both. Only LimaClient is safe to reference, since it's always built.
		_, err := NewNodeClient("")

		Expect(err).NotTo(HaveOccurred())
	})

	It("only allows the container backend on macOS", func() {
		_, err := NewNodeClient("container")

		if runtime.GOOS == "darwin" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
	})

	It("only allows the incus backend on Linux", func() {
		_, err := NewNodeClient("incus")

		if runtime.GOOS == "linux" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
	})
})
