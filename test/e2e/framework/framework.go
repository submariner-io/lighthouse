package framework

import (
	"fmt"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/federate/kubefed"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace.
type Framework struct {
	*framework.Framework
}

// New makes a new framework and sets up a BeforeEach/AfterEach
func New(baseName string) *Framework {
	f := &Framework{Framework: framework.NewFramework(baseName)}
	ginkgo.BeforeEach(f.beforeEach)
	return f
}

func (f *Framework) beforeEach() {
	if !f.SkipNamespaceCreation {
		ginkgo.By(fmt.Sprintf("Federating Namespace %q", f.Namespace))

		namespace, err := framework.KubeClients[framework.ClusterA].CoreV1().Namespaces().Get(f.Namespace, metav1.GetOptions{})
		Expect(err).To(Succeed(), "Error retrieving namespace %q", f.Namespace)
		namespace.Namespace = namespace.GetObjectMeta().GetName()

		federator := f.buildKubeFedFederator(framework.RestConfigs[framework.ClusterA])
		err = federator.Distribute(namespace)
		Expect(err).To(Succeed(), "Error distributing namespace %v", namespace)
	}
}

func (f *Framework) buildKubeFedFederator(restConfig *rest.Config) federate.Federator {
	federator, err := kubefed.New(restConfig, make(chan struct{}))
	if err != nil {
		klog.Fatalf("Error creating kubefed federator: %s", err.Error())
	}
	return federator
}
