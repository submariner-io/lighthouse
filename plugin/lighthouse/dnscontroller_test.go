package lighthouse

import (
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Lighthouse plugin controller", func() {
	klog.InitFlags(nil)

	Describe("MultiClusterService lifecycle notifications", testMCSLifecycleNotifications)
})

func testMCSLifecycleNotifications() {
	const nameSpace1 = "testNS1"
	const serviceName1 = "service1"

	var (
		multiClusterService *lighthousev1.MultiClusterService
		controller          *DNSController
		fakeClientset       lighthouseClientset.Interface
	)

	BeforeEach(func() {
		multiClusterService = newMultiClusterService("1", nameSpace1, serviceName1)
		mcsMap := new(multiClusterServiceMap)
		controller = newController(mcsMap)
		fakeClientset = fakeClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return fakeClientset, nil
		}
		Expect(controller.start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.stop()
	})

	createService := func(mcService *lighthousev1.MultiClusterService) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(mcService.Namespace).Create(mcService)
		return err
	}

	updateService := func(mcService *lighthousev1.MultiClusterService) error {
		_, err := fakeClientset.LighthouseV1().MultiClusterServices(mcService.Namespace).Update(mcService)
		return err
	}

	deleteService := func(mcService *lighthousev1.MultiClusterService) error {
		err := fakeClientset.LighthouseV1().MultiClusterServices(mcService.Namespace).Delete(mcService.Name, &metav1.DeleteOptions{})
		return err
	}

	testOnAdd := func(mcsService *lighthousev1.MultiClusterService) {
		Expect(createService(mcsService)).To(Succeed())

		verifyCachedMultiClusterService(controller, mcsService)
	}

	testOnUpdate := func(mcsService *lighthousev1.MultiClusterService) {
		Expect(updateService(mcsService)).To(Succeed())

		verifyCachedMultiClusterService(controller, mcsService)
	}

	testOnRemove := func(mcsService *lighthousev1.MultiClusterService) {
		testOnAdd(mcsService)

		Expect(deleteService(multiClusterService)).To(Succeed())

		Eventually(func() bool {
			_, ok := controller.multiClusterServices.get(mcsService.Namespace, mcsService.Name)
			return ok
		}).Should(BeFalse())
	}

	When("a MultiClusterService is added", func() {
		It("it should be added to the MultiClusterService map", func() {
			testOnAdd(multiClusterService)
		})
	})

	When("a MultiClusterService is updated", func() {
		It("it should be updated in the MultiClusterService map", func() {
			testOnAdd(multiClusterService)
			testOnUpdate(newMultiClusterService("2", nameSpace1, serviceName1))
		})
	})

	When("a MultiClusterService is deleted", func() {
		It("it should be removed to the MultiClusterService map", func() {
			testOnRemove(multiClusterService)
		})
	})
}

func verifyCachedMultiClusterService(controller *DNSController, expected *lighthousev1.MultiClusterService) {
	Eventually(func() *lighthousev1.MultiClusterServiceSpec {
		mcs, ok := controller.multiClusterServices.get(expected.Namespace, expected.Name)
		if ok {
			return &mcs.Spec
		}
		return nil
	}).Should(Equal(&expected.Spec))
}

func newMultiClusterService(id string, namespace string, name string) *lighthousev1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
