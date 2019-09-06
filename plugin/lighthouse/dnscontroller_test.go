package lighthouse

import (
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeMCSClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Lighthouse Controller", func() {
	klog.InitFlags(nil)

	Describe("Lighthouse MulticlusterService Creation", testMCSServiceCRUD)
})

func testMCSServiceCRUD() {
	const nameSpace1 = "testNS2"
	const serviceName1 = "service1"
	const nameSpace2 = "testNS2"
	const serviceName2 = "service2"
	var (
		multiClusterService         *lighthousev1.MultiClusterService
		multiClusterService2        *lighthousev1.MultiClusterService
		updatedMultiClusterService1 *lighthousev1.MultiClusterService
		controller                  *DNSController
		fakeClientset               mcsClientset.Interface
	)

	BeforeEach(func() {
		multiClusterService = newMultiClusterService("1", nameSpace1, serviceName1)
		rsMap := make(remoteServiceMap)
		controller = New(rsMap)
		fakeClientset = fakeMCSClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (mcsClientset.Interface, error) {
			return fakeClientset, nil
		}
		Expect(controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.Stop()
	})

	createService := func(mcService *lighthousev1.MultiClusterService) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace1).Create(mcService)
		return err
	}

	deleteService := func(mcService *lighthousev1.MultiClusterService) error {
		err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace1).Delete(mcService.Name, &metav1.DeleteOptions{})
		return err
	}

	updateService := func(mcService *lighthousev1.MultiClusterService) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace1).Update(mcService)
		return err
	}

	When("a MultiClusterService is added", func() {
		It("the remote service map should have the MultiClusterService", func() {
			Expect(createService(multiClusterService)).To(Succeed())
			key := multiClusterService.Namespace + "/" + multiClusterService.Name
			Eventually(controller.remoteServiceMap).Should(HaveKey(key))
			Eventually(controller.remoteServiceMap[key]).Should(BeEquivalentTo(multiClusterService))
		})
	})

	When("a MultiClusterService is deleted", func() {
		It("the remote service map should not have  the MultiClusterService", func() {
			multiClusterService2 = newMultiClusterService("1", nameSpace2, serviceName2)
			Expect(createService(multiClusterService)).To(Succeed())
			Expect(createService(multiClusterService2)).To(Succeed())
			Expect(deleteService(multiClusterService)).To(Succeed())
			key1 := multiClusterService.Namespace + "/" + multiClusterService.Name
			key2 := multiClusterService2.Namespace + "/" + multiClusterService2.Name
			Eventually(controller.remoteServiceMap).ShouldNot(HaveKey(key1))
			Eventually(controller.remoteServiceMap).Should(HaveKey(key2))

		})
	})

	When("a MultiClusterService is updated", func() {
		It("the remote service map should have the updated MultiClusterService", func() {
			updatedMultiClusterService1 = newMultiClusterService("2", nameSpace1, serviceName1)
			Expect(createService(multiClusterService)).To(Succeed())
			Expect(updateService(updatedMultiClusterService1)).To(Succeed())
			key := multiClusterService.Namespace + "/" + multiClusterService.Name
			Eventually(controller.remoteServiceMap).Should(HaveKey(key))
			Eventually(controller.remoteServiceMap[key]).Should(BeEquivalentTo(updatedMultiClusterService1))
		})
	})
}

func newMultiClusterService(id string, nameSpace string, serviceName string) *lighthousev1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: nameSpace,
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				lighthousev1.ClusterServiceInfo{
					ClusterID: "cluster" + id,
					ServiceIP: "192.168.56.2" + id,
				},
			},
		},
	}
}
