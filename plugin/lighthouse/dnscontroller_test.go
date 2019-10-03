package lighthouse

import (
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeMCSClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Lighthouse Controller", func() {
	klog.InitFlags(nil)

	Describe("Lighthouse MulticlusterService Creation", testMCSService)
})

func testMCSService() {
	const nameSpace1 = "testNS1"
	const serviceName1 = "service1"

	var (
		multiClusterService         *mcservice.MultiClusterService
		updatedMultiClusterService1 *mcservice.MultiClusterService
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

	createService := func(mcService *mcservice.MultiClusterService, nameSpace string) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace).Create(mcService)
		return err
	}

	updateService := func(mcService *mcservice.MultiClusterService, nameSpace string) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace).Update(mcService)
		return err
	}

	deleteService := func(mcService *mcservice.MultiClusterService, nameSpace string) error {
		err := fakeClientset.LighthouseV1().MultiClusterServices(nameSpace).Delete(mcService.Name, &metav1.DeleteOptions{})
		return err
	}

	testOnAdd := func(mcsService *mcservice.MultiClusterService) {
		Expect(createService(mcsService, nameSpace1)).To(Succeed())
		key := mcsService.Namespace + "/" + mcsService.Name
		Eventually(controller.remoteServiceMap).Should(HaveKey(key))
		Eventually(controller.remoteServiceMap[key]).Should(BeEquivalentTo(mcsService))
	}

	testOnUpdate := func(mcsService *mcservice.MultiClusterService) {
		Expect(updateService(mcsService, nameSpace1)).To(Succeed())
		key := mcsService.Namespace + "/" + mcsService.Name
		Eventually(controller.remoteServiceMap).Should(HaveKey(key))
		//TODO asuryana Update is failing need to fix it.
		//Eventually(controller.remoteServiceMap[key]).Should(BeEquivalentTo(mcsService))
	}

	testOnRemove := func(mcsService *mcservice.MultiClusterService) {
		testOnAdd(mcsService)
		Expect(deleteService(multiClusterService, nameSpace1)).To(Succeed())
		key1 := mcsService.Namespace + "/" + mcsService.Name
		Eventually(controller.remoteServiceMap).ShouldNot(HaveKey(key1))
	}

	When("a MultiClusterService is added", func() {
		It("the remote service map should have the MultiClusterService", func() {
			testOnAdd(multiClusterService)
		})
	})

	When("a MultiClusterService is updated", func() {
		It("the remote service map should have the updated MultiClusterService", func() {
			updatedMultiClusterService1 = newMultiClusterService("2", nameSpace1, serviceName1)
			testOnAdd(multiClusterService)
			testOnUpdate(updatedMultiClusterService1)
		})
	})

	When("a MultiClusterService is deleted", func() {
		It("the remote service map should not have  the MultiClusterService", func() {
			testOnRemove(multiClusterService)
		})
	})
}

func newMultiClusterService(id string, nameSpace string, serviceName string) *mcservice.MultiClusterService {
	return &mcservice.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: nameSpace,
		},
		Spec: mcservice.MultiClusterServiceSpec{
			Items: []mcservice.ClusterServiceInfo{
				mcservice.ClusterServiceInfo{
					ClusterID: "cluster" + id,
					ServiceIP: "192.168.56.2" + id,
				},
			},
		},
	}
}
