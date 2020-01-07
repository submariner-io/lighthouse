package multiclusterservice

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestMultiClusterService(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MultiClusterService Suite")
}
