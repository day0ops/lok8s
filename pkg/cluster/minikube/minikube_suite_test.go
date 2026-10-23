package minikube

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMinikube(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Minikube Suite")
}
