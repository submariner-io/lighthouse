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

	Describe("ServiceImport lifecycle notifications", testLifecycleNotifications)
	Describe("ServiceImport round robin load balancing", testRoundRobinSelection)

})

func testLifecycleNotifications() {
	const (
		service1   = "service1"
		namespace1 = "namespace1"
		serviceIP  = "192.168.56.21"
		serviceIP2 = "192.168.56.22"
		clusterID  = "clusterID"
		clusterID2 = "clusterID2"
	)

	var (
		serviceImport *lighthousev2a1.ServiceImport
		controller    *Controller
		fakeClientset lighthouseClientset.Interface
		mockCs        *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		mockCs.clusterStatusMap[clusterID2] = true
		serviceImport = newServiceImport(namespace1, service1, serviceIP, clusterID)
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

		verifyCachedServiceImport(controller, serviceImport, mockCs)
	}

	testOnUpdate := func(serviceImport *lighthousev2a1.ServiceImport) {
		Expect(updateService(serviceImport)).To(Succeed())

		verifyCachedServiceImport(controller, serviceImport, mockCs)
	}

	testOnRemove := func(serviceImport *lighthousev2a1.ServiceImport) {
		testOnAdd(serviceImport)

		Expect(deleteService(serviceImport)).To(Succeed())

		Eventually(func() bool {
			_, ok := controller.serviceImports.SelectIP(serviceImport.Namespace, serviceImport.Name, mockCs.IsConnected)
			return ok
		}).Should(BeFalse())
	}

	testOnDoubleAdd := func(first *lighthousev2a1.ServiceImport, second *lighthousev2a1.ServiceImport) {
		Expect(createService(first)).To(Succeed())
		Expect(createService(second)).To(Succeed())

		verifyUpdatedCachedServiceImport(controller, first, second, mockCs)
	}

	When("a ServiceImport is added", func() {
		It("it should be added to the ServiceImport map", func() {
			testOnAdd(serviceImport)
		})
	})

	When("a ServiceImport is updated", func() {
		It("it should be updated in the ServiceImport map", func() {
			testOnAdd(serviceImport)
			testOnUpdate(newServiceImport(namespace1, service1, serviceIP2, clusterID))
		})
	})

	When("same ServiceImport is added in another cluster", func() {
		It("it should be added to existing ServiceImport map", func() {
			testOnDoubleAdd(serviceImport, newServiceImport(namespace1, service1, serviceIP2, clusterID2))
		})
	})

	When("a ServiceImport is deleted", func() {
		It("it should be removed to the ServiceImport map", func() {
			testOnRemove(serviceImport)
		})
	})
}

func testRoundRobinSelection() {
	const (
		service1   = "service1"
		namespace1 = "namespace1"
		namespace2 = "namespace2"
		serviceIP  = "192.168.56.21"
		serviceIP2 = "192.168.56.22"
		serviceIP3 = "192.168.56.23"
		clusterID  = "clusterID"
		clusterID2 = "clusterID2"
		clusterID3 = "clusterID3"
	)

	var (
		serviceImport *lighthousev2a1.ServiceImport
		controller    *Controller
		fakeClientset lighthouseClientset.Interface
		mockCs        *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		serviceImport = newServiceImport(namespace1, service1, serviceIP, clusterID)
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

	When("single service is present in only one cluster and it is connected", func() {
		It("should return the same ip everytime", func() {
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)

			Eventually(func() bool {
				first, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				second, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				return (first == serviceIP) && (second == serviceIP)
			}).Should(BeTrue())
		})
	})

	When("single service is present in 2 clusters with 2 ips and both are connected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = true
			mockCs.clusterStatusMap[clusterID2] = true
		})
		It("should return the different ip everytime", func() {
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
			si := newServiceImport(namespace1, service1, serviceIP2, clusterID2)
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(si.Namespace).Create(si)

			Eventually(func() bool {
				first, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				second, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				third, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				return (first == serviceIP) && (second == serviceIP2) && (third == serviceIP)
			}).Should(BeTrue())
		})
	})

	When("single service is present in 2 clusters with 2 ips and one is connected and the other not", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
			mockCs.clusterStatusMap[clusterID2] = true
		})
		It("should return the ip of the connected cluster everytime", func() {
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
			si := newServiceImport(namespace1, service1, serviceIP2, clusterID2)
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(si.Namespace).Create(si)

			Eventually(func() bool {
				first, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				second, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				return (first == serviceIP2) && (second == serviceIP2)
			}).Should(BeTrue())
		})
	})

	When("single service is present in 2 clusters with 2 ips in different namespace in the same cluster which is connected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = true
		})
		It("should return the same ip for each namespace", func() {
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
			si := newServiceImport(namespace2, service1, serviceIP2, clusterID)
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(si.Namespace).Create(si)

			Eventually(func() bool {
				first, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				second, _ := controller.serviceImports.SelectIP(namespace2, service1, mockCs.IsConnected)
				third, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				forth, _ := controller.serviceImports.SelectIP(namespace2, service1, mockCs.IsConnected)
				return (first == serviceIP) && (second == serviceIP2) && (third == serviceIP) && (forth == serviceIP2)
			}).Should(BeTrue())
		})
	})

	When("single service is present in 3 clusters with 3 ips in 3 clusters which are connected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = true
			mockCs.clusterStatusMap[clusterID2] = true
			mockCs.clusterStatusMap[clusterID3] = true
		})
		It("should return ip for each service in its turn according to rr", func() {
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
			si1 := newServiceImport(namespace1, service1, serviceIP2, clusterID2)
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(si1.Namespace).Create(si1)
			si2 := newServiceImport(namespace1, service1, serviceIP3, clusterID3)
			_, _ = fakeClientset.LighthouseV2alpha1().ServiceImports(si2.Namespace).Create(si2)

			Eventually(func() bool {
				first, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				second, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				third, _ := controller.serviceImports.SelectIP(namespace1, service1, mockCs.IsConnected)
				return (first == serviceIP) && (second == serviceIP2) && (third == serviceIP3)
			}).Should(BeTrue())
		})
	})
}

func verifyCachedServiceImport(controller *Controller, expected *lighthousev2a1.ServiceImport, m *MockClusterStatus) {
	Eventually(func() string {
		name := expected.Annotations["origin-name"]
		namespace := expected.Annotations["origin-namespace"]
		selectedIp, _ := controller.serviceImports.SelectIP(namespace, name, m.IsConnected)
		return selectedIp
	}).Should(Equal(expected.Status.Clusters[0].IPs[0]))
}

func verifyUpdatedCachedServiceImport(controller *Controller, first, second *lighthousev2a1.ServiceImport, m *MockClusterStatus) {
	// We can't just compare first and second coz map iteration order is not fixed
	Eventually(func() bool {
		name := first.Annotations["origin-name"]
		namespace := first.Annotations["origin-namespace"]
		selectedIp1, ok1 := controller.serviceImports.SelectIP(namespace, name, m.IsConnected)
		selectedIp2, ok2 := controller.serviceImports.SelectIP(namespace, name, m.IsConnected)
		if ok1 && ok2 {
			return validateIpList(first, second, []string{selectedIp1, selectedIp2})
		}
		return false
	}).Should(BeTrue())
}

func validateIpList(first, second *lighthousev2a1.ServiceImport, ipList []string) bool {
	firstClusterInfo := first.Status.Clusters[0]
	secondClusterInfo := second.Status.Clusters[0]
	ips := []string{firstClusterInfo.IPs[0], secondClusterInfo.IPs[0]}
	sort.Strings(ips)
	sort.Strings(ipList)

	return reflect.DeepEqual(ipList, ips)
}

type MockClusterStatus struct {
	clusterStatusMap map[string]bool
}

func NewMockClusterStatus() *MockClusterStatus {
	return &MockClusterStatus{clusterStatusMap: make(map[string]bool)}
}

func (m *MockClusterStatus) IsConnected(clusterId string) bool {
	return m.clusterStatusMap[clusterId]
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
