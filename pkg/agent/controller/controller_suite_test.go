package controller_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

func init() {
	klog.InitFlags(nil)

	err := lighthousev2a1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Controller Suite")
}
