package loadbalance_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestLoadBalance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LoadBalance Suite")
}
