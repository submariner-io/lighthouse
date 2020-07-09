package framework

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
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

func (f *Framework) AwaitServiceExportStatusCondition(cluster framework.ClusterIndex, name string, namespace string) *v2alpha1.ServiceExport {
	se := LighthouseClients[cluster].LighthouseV2alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Retrieving serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	serviceExport := framework.AwaitUntil("retrieve serviceExport", func() (interface{}, error) {
		return se.Get(name, metav1.GetOptions{})

	}, func(result interface{}) (bool, string, error) {
		se := result.(*v2alpha1.ServiceExport)
		if len(se.Status.Conditions) == 0 {
			return false, "Status.Conditions is empty", nil
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
}

func (f *Framework) GetService(cluster framework.ClusterIndex, name string, namespace string) (*v1.Service, error) {
	By(fmt.Sprintf("Retrieving service %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	return framework.KubeClients[cluster].CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

func (f *Framework) AwaitServiceImportIP(targetCluster framework.ClusterIndex, sourceCluster framework.ClusterIndex, svc *v1.Service) *lighthousev2a1.ServiceImport {
	var serviceIP string

	if framework.TestContext.GlobalnetEnabled {
		serviceIP = svc.Annotations[submarinerIpamGlobalIp]
	} else {
		serviceIP = svc.Spec.ClusterIP
	}
	siName := svc.Name + "-" + svc.Namespace + "-" + framework.TestContext.ClusterIDs[sourceCluster]
	si := LighthouseClients[targetCluster].LighthouseV2alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	By(fmt.Sprintf("Retrieving ServiceImport %s on %q", siName, framework.TestContext.ClusterIDs[targetCluster]))
	return framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		return si.Get(siName, metav1.GetOptions{})

	}, func(result interface{}) (bool, string, error) {
		si := result.(*lighthousev2a1.ServiceImport)
		if si.Status.Clusters[0].IPs[0] != serviceIP {
			return false, fmt.Sprintf("ServiceImportIP %s doesn't match %s", si.Status.Clusters[0].IPs[0], serviceIP), nil
		}
		return true, "", nil
	}).(*lighthousev2a1.ServiceImport)
}

func (f *Framework) AwaitServiceImportDelete(targetCluster framework.ClusterIndex, sourceCluster framework.ClusterIndex, name string, namespace string) {
	siName := name + "-" + namespace + "-" + framework.TestContext.ClusterIDs[sourceCluster]
	si := LighthouseClients[targetCluster].LighthouseV2alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		_, err := si.Get(siName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, func(result interface{}) (bool, string, error) {
		return result.(bool), "", nil
	})
}

func (f *Framework) AwaitGlobalnetIP(cluster framework.ClusterIndex, name string, namespace string) string {
	if framework.TestContext.GlobalnetEnabled {
		svc := framework.KubeClients[cluster].CoreV1().Services(namespace)
		svcObj := framework.AwaitUntil("retrieve service", func() (interface{}, error) {
			return svc.Get(name, metav1.GetOptions{})

		}, func(result interface{}) (bool, string, error) {
			svc := result.(*v1.Service)
			globalIp := svc.Annotations[submarinerIpamGlobalIp]
			if globalIp == "" {
				return false, "GlobalIP not available", nil
			}
			return true, "", nil
		}).(*v1.Service)
		return svcObj.Annotations[submarinerIpamGlobalIp]
	}
	return ""
}
