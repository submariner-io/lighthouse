package endpointslice_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/klog"
)

func init() {
	klog.InitFlags(nil)
}

func TestEndpointSlice(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EndpointSlice Suite")
}
