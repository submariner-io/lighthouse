package framework

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
)

const (
	cleanupWait = time.Second * 5
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
type Framework struct {
	*framework.Framework
}

var LighthouseClients []*lighthouseClientset.Clientset

func init() {
	framework.AddBeforeSuite(beforeSuite)
}

// NewFramework creates a test framework.
func NewFramework(baseName string) *Framework {
	f := &Framework{Framework: framework.NewFramework(baseName)}
	return f
}

func beforeSuite() {
	By("Creating lighthouse clients")

	for _, restConfig := range framework.RestConfigs {
		LighthouseClients = append(LighthouseClients, createLighthouseClient(restConfig))
	}
}

func createLighthouseClient(restConfig *rest.Config) *lighthouseClientset.Clientset {
	clientSet, err := lighthouseClientset.NewForConfig(restConfig)
	Expect(err).To(Not(HaveOccurred()))
	return clientSet
}

func (f *Framework) NewServiceExport(cluster framework.ClusterIndex, name string, namespace string) *v2alpha1.ServiceExport {
	nginxServiceExport := v2alpha1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	se := LighthouseClients[cluster].LighthouseV2alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Creating serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	serviceExport := framework.AwaitUntil("create serviceExport", func() (interface{}, error) {
		return se.Create(&nginxServiceExport)

	}, framework.NoopCheckResult).(*v2alpha1.ServiceExport)
	return serviceExport
}

func (f *Framework) GetServiceExport(cluster framework.ClusterIndex, name string, namespace string) *v2alpha1.ServiceExport {
	se := LighthouseClients[cluster].LighthouseV2alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Retrieving serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	serviceExport := framework.AwaitUntil("retrieve serviceExport", func() (interface{}, error) {
		return se.Get(name, metav1.GetOptions{})

	}, func(result interface{}) (bool, string, error) {
		se := result.(*v2alpha1.ServiceExport)
		if len(se.Status.Conditions) == 0 {
			return false, fmt.Sprintf("expected Status to be non-empty"), nil
		}
		return true, "", nil
	}).(*v2alpha1.ServiceExport)
	return serviceExport
}

func (f *Framework) DeleteServiceExport(cluster framework.ClusterIndex, name string, namespace string) {
	By(fmt.Sprintf("Deleting serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	framework.AwaitUntil("delete service export", func() (interface{}, error) {
		return nil, LighthouseClients[cluster].LighthouseV2alpha1().ServiceExports(namespace).Delete(name, &metav1.DeleteOptions{})
	}, framework.NoopCheckResult)
	// Give time for ServiceExport cleanup to propagate
	//TODO: Add a WaitUntilDeleted method for this
	By("Waiting for mcs cleanup...")
	time.Sleep(cleanupWait)
}
