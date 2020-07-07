package serviceimport

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestServiceImport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ServiceImport	 Suite")
}
