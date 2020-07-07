package serviceimport

import (
	"reflect"
	"sort"

	"k8s.io/client-go/rest"
	"k8s.io/klog"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ServiceImport controller", func() {
	klog.InitFlags(nil)

	Describe("ServiceImport lifecycle notifications", testMCSLifecycleNotifications)
})

func testMCSLifecycleNotifications() {
	const nameSpace1 = "testNS1"
	const serviceName1 = "service1"

	var (
		serviceImport *lighthousev2a1.ServiceImport
		controller    *Controller
		fakeClientset lighthouseClientset.Interface
	)

	BeforeEach(func() {
		serviceImport = newServiceImport(nameSpace1, serviceName1, "192.168.56.21", "cluster1")
		serviceImportMap := NewMap()
		controller = NewController(serviceImportMap)
		fakeClientset = fakeClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return fakeClientset, nil
		}
		Expect(controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.Stop()
	})

	createService := func(serviceImport *lighthousev2a1.ServiceImport) error {
		_, err := fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
		return err
	}

	updateService := func(serviceImport *lighthousev2a1.ServiceImport) error {
		_, err := fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Update(serviceImport)
		return err
	}

	deleteService := func(serviceImport *lighthousev2a1.ServiceImport) error {
		err := fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Delete(serviceImport.Name, &metav1.DeleteOptions{})
		return err
	}

	testOnAdd := func(serviceImport *lighthousev2a1.ServiceImport) {
		Expect(createService(serviceImport)).To(Succeed())

		verifyCachedServiceImport(controller, serviceImport)
	}

	testOnUpdate := func(serviceImport *lighthousev2a1.ServiceImport) {
		Expect(updateService(serviceImport)).To(Succeed())

		verifyCachedServiceImport(controller, serviceImport)
	}

	testOnRemove := func(serviceImport *lighthousev2a1.ServiceImport) {
		testOnAdd(serviceImport)

		Expect(deleteService(serviceImport)).To(Succeed())

		Eventually(func() bool {
			_, ok := controller.serviceImports.GetIps(serviceImport.Namespace, serviceImport.Name)
			return ok
		}).Should(BeFalse())
	}

	testOnDoubleAdd := func(first *lighthousev2a1.ServiceImport, second *lighthousev2a1.ServiceImport) {
		Expect(createService(first)).To(Succeed())
		Expect(createService(second)).To(Succeed())

		verifyUpdatedCachedServiceImport(controller, first, second)
	}

	When("a ServiceImport is added", func() {
		It("it should be added to the ServiceImport map", func() {
			testOnAdd(serviceImport)
		})
	})

	When("a ServiceImport is updated", func() {
		It("it should be updated in the ServiceImport map", func() {
			testOnAdd(serviceImport)
			testOnUpdate(newServiceImport(nameSpace1, serviceName1, "192.168.56.22", "cluster1"))
		})
	})

	When("same ServiceImport is added in another cluster", func() {
		It("it should be added to existing ServiceImport map", func() {
			testOnDoubleAdd(serviceImport, newServiceImport(nameSpace1, serviceName1, "192.168.56.22", "cluster2"))
		})
	})

	When("a ServiceImport is deleted", func() {
		It("it should be removed to the ServiceImport map", func() {
			testOnRemove(serviceImport)
		})
	})
}

func verifyCachedServiceImport(controller *Controller, expected *lighthousev2a1.ServiceImport) {
	Eventually(func() []string {
		name := expected.Annotations["origin-name"]
		namespace := expected.Annotations["origin-namespace"]
		ipList, ok := controller.serviceImports.GetIps(namespace, name)
		if ok {
			return ipList
		}
		return nil
	}).Should(Equal([]string{expected.Status.Clusters[0].IPs[0]}))
}

func verifyUpdatedCachedServiceImport(controller *Controller, first *lighthousev2a1.ServiceImport, second *lighthousev2a1.ServiceImport) {
	// We can't just compare first and second coz map iteration order is not fixed
	Eventually(func() bool {
		name := first.Annotations["origin-name"]
		namespace := first.Annotations["origin-namespace"]
		ipList, ok := controller.serviceImports.GetIps(namespace, name)
		if ok {
			return validateIpList(first, second, ipList)
		}
		return false
	}).Should(BeTrue())
}

func validateIpList(first *lighthousev2a1.ServiceImport, second *lighthousev2a1.ServiceImport, ipList []string) bool {
	firstClusterInfo := first.Status.Clusters[0]
	secondClusterInfo := second.Status.Clusters[0]
	ips := []string{firstClusterInfo.IPs[0], secondClusterInfo.IPs[0]}
	sort.Strings(ips)
	sort.Strings(ipList)
	return reflect.DeepEqual(ipList, ips)
}

func newServiceImport(namespace, name, serviceIP, clusterID string) *lighthousev2a1.ServiceImport {
	return &lighthousev2a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: namespace,
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
		},
		Status: lighthousev2a1.ServiceImportStatus{
			Clusters: []lighthousev2a1.ClusterStatus{
				{
					Cluster: clusterID,
					IPs:     []string{serviceIP},
				},
			},
		},
	}
}
