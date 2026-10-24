package native

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("gatewayFromSubnet", func() {
	It("derives the gateway as the network address plus one", func() {
		gateway, prefix, err := gatewayFromSubnet("10.89.0.0/16")

		Expect(err).NotTo(HaveOccurred())
		Expect(gateway.String()).To(Equal("10.89.0.1"))
		Expect(prefix).To(Equal(16))
	})

	It("errors on an invalid CIDR", func() {
		_, _, err := gatewayFromSubnet("not-a-cidr")

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("cidrFromGatewayNetmask", func() {
	It("is the inverse of gatewayFromSubnet", func() {
		cidr, err := cidrFromGatewayNetmask("10.89.0.1", "255.255.0.0")

		Expect(err).NotTo(HaveOccurred())
		Expect(cidr).To(Equal("10.89.0.0/16"))
	})

	It("errors on an invalid gateway", func() {
		_, err := cidrFromGatewayNetmask("not-an-ip", "255.255.0.0")

		Expect(err).To(HaveOccurred())
	})

	It("errors on an invalid netmask", func() {
		_, err := cidrFromGatewayNetmask("10.89.0.1", "not-a-mask")

		Expect(err).To(HaveOccurred())
	})
})
