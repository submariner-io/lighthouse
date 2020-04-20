package multiclusterservice

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

var _ = Describe("MultiClusterService controller", func() {
	klog.InitFlags(nil)

	Describe("MultiClusterService lifecycle notifications", testMCSLifecycleNotifications)
})

func testMCSLifecycleNotifications() {
	const nameSpace1 = "testNS1"
	const serviceName1 = "service1"

	var (
		multiClusterService *lighthousev1.MultiClusterService
		controller          *Controller
		fakeClientset       lighthouseClientset.Interface
	)

	BeforeEach(func() {
		multiClusterService = newMultiClusterService(nameSpace1, serviceName1, "192.168.56.21", "cluster1")
		mcsMap := NewMap()
		controller = NewController(mcsMap)
		fakeClientset = fakeClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return fakeClientset, nil
		}
		Expect(controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.Stop()
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
			_, ok := controller.multiClusterServices.Get(mcsService.Namespace, mcsService.Name)
			return ok
		}).Should(BeFalse())
	}

	testOnDoubleAdd := func(first *lighthousev1.MultiClusterService, second *lighthousev1.MultiClusterService) {
		Expect(createService(first)).To(Succeed())
		Expect(createService(second)).To(Succeed())

		verifyUpdatedCachedMultiClusterService(controller, first, second)
	}

	When("a MultiClusterService is added", func() {
		It("it should be added to the MultiClusterService map", func() {
			testOnAdd(multiClusterService)
		})
	})

	When("a MultiClusterService is updated", func() {
		It("it should be updated in the MultiClusterService map", func() {
			testOnAdd(multiClusterService)
			testOnUpdate(newMultiClusterService(nameSpace1, serviceName1, "192.168.56.22", "cluster1"))
		})
	})

	When("same MultiClusterService is added in another cluster", func() {
		It("it should be added to existing MultiClusterService map", func() {
			testOnDoubleAdd(multiClusterService, newMultiClusterService(nameSpace1, serviceName1, "192.168.56.22", "cluster2"))
		})
	})

	When("a MultiClusterService is deleted", func() {
		It("it should be removed to the MultiClusterService map", func() {
			testOnRemove(multiClusterService)
		})
	})
}

func verifyCachedMultiClusterService(controller *Controller, expected *lighthousev1.MultiClusterService) {
	Eventually(func() *RemoteService {
		name := expected.Annotations["origin-name"]
		namespace := expected.Annotations["origin-namespace"]
		mcs, ok := controller.multiClusterServices.Get(namespace, name)
		if ok {
			return mcs
		}
		return nil
	}).Should(Equal(newRemoteService(expected)))
}

func verifyUpdatedCachedMultiClusterService(controller *Controller, first *lighthousev1.MultiClusterService, second *lighthousev1.MultiClusterService) {
	// We can't just compare first and second coz map iteration order is not fixed
	Eventually(func() bool {
		name := first.Annotations["origin-name"]
		namespace := first.Annotations["origin-namespace"]
		rs, ok := controller.multiClusterServices.Get(namespace, name)
		if ok {
			return validateRemoteService(first, second, rs)
		}
		return false
	}).Should(BeTrue())
}

func validateRemoteService(first *lighthousev1.MultiClusterService, second *lighthousev1.MultiClusterService, rs *RemoteService) bool {
	firstClusterInfo := first.Spec.Items[0]
	secondClusterInfo := second.Spec.Items[0]
	return rs.ClusterInfo[firstClusterInfo.ClusterID] == firstClusterInfo.ServiceIP && rs.ClusterInfo[secondClusterInfo.ClusterID] == secondClusterInfo.ServiceIP
}

func newMultiClusterService(namespace, name, serviceIP, clusterID string) *lighthousev1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: namespace,
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				{
					ClusterID: clusterID,
					ServiceIP: serviceIP,
				},
			},
		},
	}
}

func newRemoteService(mcs *lighthousev1.MultiClusterService) *RemoteService {
	name := mcs.Annotations["origin-name"]
	namespace := mcs.Annotations["origin-namespace"]
	remoteService := &RemoteService{
		key:         namespace + "/" + name,
		ClusterInfo: map[string]string{},
	}
	for _, info := range mcs.Spec.Items {
		remoteService.ClusterInfo[info.ClusterID] = info.ServiceIP
	}
	remoteService.IpList = make([]string, 0)
	for _, v := range remoteService.ClusterInfo {
		remoteService.IpList = append(remoteService.IpList, v)
	}
	return remoteService
}
