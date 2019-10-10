package controller

import (
	"errors"
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/federate/mocks"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	fakeCoreClientSet "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	serviceName1 = "serviceName1"
	serviceName2 = "serviceName2"
	serviceNS1   = "serviceNS1"
	serviceNS2   = "serviceNS2"
	serviceIP1   = "192.168.56.1"
	serviceIP2   = "192.168.56.2"
	serviceIP3   = "192.168.56.3"
	eastCluster  = "east"
	westCluster  = "west"
	northCluster = "north"
)

type watchReactor struct {
	multiclusterServiceWatchStarted chan bool
}

type testDriver struct {
	distributeCalled  chan bool
	deleteCalled      chan bool
	mockCtrl          *gomock.Controller
	mockFederator     *mocks.MockFederator
	controller        *LightHouseController
	fakeClientsets    map[string]*fakeCoreClientSet.Clientset
	capturedMCService **lighthousev1.MultiClusterService
}

var _ = Describe("Lighthouse Controller", func() {
	klog.InitFlags(nil)

	When("start is called", testStart)
	Describe("Cluster lifecycle notifications", testClusterLifecycleNotifications)
	Describe("Lighthouse resource distribution", testResourceDistribution)
})

func testStart() {
	var (
		mockCtrl      *gomock.Controller
		mockFederator *mocks.MockFederator
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockFederator = mocks.NewMockFederator(mockCtrl)
	})

	AfterEach(func() {
		mockCtrl.Finish()
		mockCtrl = nil
		mockFederator = nil
	})

	When("WatchClusters succeeds", func() {
		It("should return no error", func() {
			mockFederator.EXPECT().WatchClusters(gomock.Any())
			Expect(New(mockFederator).Start()).To(Succeed())
		})
	})

	When("WatchClusters fails", func() {
		It("should return an error", func() {
			mockFederator.EXPECT().WatchClusters(gomock.Any()).Return(errors.New("mock error"))
			Expect(New(mockFederator).Start()).ToNot(Succeed())
		})
	})
}

func testClusterLifecycleNotifications() {
	var (
		controller   *LightHouseController
		watchReactor *watchReactor
	)

	BeforeEach(func() {
		controller = New(mocks.NewMockFederator(gomock.NewController(GinkgoT())))
		watchReactor = newWatchReactor(controller)
	})

	AfterEach(func() {
		watchReactor.close()
		controller.Stop()
		watchReactor = nil
		controller = nil
	})

	testOnAdd := func(clusterID string) {
		controller.OnAdd(clusterID, &rest.Config{})

		Expect(controller.remoteClusters).Should(HaveKey(clusterID))
		Eventually(watchReactor.multiclusterServiceWatchStarted).Should(Receive())
	}

	testOnRemove := func(clusterID string) {
		testOnAdd("east")
		stopChan := controller.remoteClusters["east"].stopCh
		servicesWorkQueue := controller.remoteClusters["east"].queue

		controller.OnRemove("east")

		Expect(controller.remoteClusters).ShouldNot(HaveKey("east"))
		Expect(stopChan).To(BeClosed())
		Eventually(servicesWorkQueue.ShuttingDown).Should(BeTrue())
	}

	testOnUpdate := func(clusterID string) {
		watchReactor.reset()
		prevStopChan := controller.remoteClusters["east"].stopCh
		prevServicesWorkQueue := controller.remoteClusters["east"].queue

		controller.OnUpdate("east", &rest.Config{})

		Expect(controller.remoteClusters).Should(HaveKey("east"))
		Eventually(watchReactor.multiclusterServiceWatchStarted).Should(Receive())
		Eventually(prevServicesWorkQueue.ShuttingDown).Should(BeTrue())
		Expect(prevStopChan).To(BeClosed())
	}

	When("a cluster is added", func() {
		It("should start a watch for the Services resources", func() {
			testOnAdd("east")
		})
	})

	When("a cluster is removed", func() {
		It("should remove and close the Services watch", func() {
			testOnRemove("east")
		})
	})

	When("a cluster is updated", func() {
		It("should restart the Services watch", func() {
			testOnAdd("east")
			testOnUpdate("east")

			// Run OnUpdate again to ensure locks were released properly
			testOnUpdate("east")
		})
	})
}

func testResourceDistribution() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDriver()
	})

	AfterEach(func() {
		t.close()
	})

	When("a Service is added with no existing MultiClusterService", func() {
		It("should create a new MultiClusterService and distribute it", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)
			t.verifyDistributedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})

			// Add another service
			t.createServiceAndVerifyDistribute(serviceName2, serviceNS1, serviceIP2, eastCluster)
			t.verifyDistributedMCService(serviceName2, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP2})
		})
	})

	When("a Service with the same name and namespace as an existing MultiClusterService is added", func() {
		It("should append its ClusterServiceInfo to the existing MultiClusterService and distribute it", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP2, westCluster)
			t.verifyDistributedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: westCluster, ServiceIP: serviceIP2})

			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)
			t.verifyDistributedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: westCluster, ServiceIP: serviceIP2},
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("a Service with an existing ClusterServiceInfo is re-added", func() {
		It("should not append its ClusterServiceInfo again and the MultiClusterService should not be distributed", func() {
			t.initMultiClusterService(serviceName1, serviceNS1, serviceIP1, eastCluster)
			t.setupExpectDistribute().MaxTimes(0)
			t.createService(serviceName1, serviceNS1, serviceIP1, eastCluster)

			Consistently(t.distributeCalled).ShouldNot(Receive(), "Distribute was unexpectedly called")
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("a Service is added with the same name but different namespace as an existing MultiClusterService", func() {
		It("should create a new MultiClusterService and distribute it", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS2, serviceIP2, eastCluster)
			t.verifyDistributedMCService(serviceName1, serviceNS2, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP2})

			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)
			t.verifyDistributedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS2), serviceName1, serviceNS2,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP2})
		})
	})

	When("a Service is added and the MultiClusterService distribute initially fails", func() {
		It("should retry until it succeeds", func() {
			// Simulate the first call to Distribute fails and the second succeeds.
			gomock.InOrder(
				t.mockFederator.EXPECT().Distribute(gomock.Any()).Return(errors.New("mock")),
				t.setupExpectDistribute())
			t.createService(serviceName1, serviceNS1, serviceIP1, eastCluster)

			Eventually(t.distributeCalled, 5).Should(Receive(), "Distribute was not retried")
			t.verifyDistributedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("the lone cluster Service contained in a MultiClusterService is deleted", func() {
		It("should delete the MultiClusterService", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)

			t.setupExpectDelete()
			t.deleteService(serviceName1, serviceNS1, eastCluster)

			Eventually(t.deleteCalled, 5).Should(Receive(), "Delete was not called")
			t.verifyDeletedMCService(serviceName1, serviceNS1)
		})
	})

	When("a cluster Service is deleted with other cluster Services remaining in the MultiClusterService", func() {
		It("should remove its ClusterServiceInfo from the MultiClusterService and distribute it", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP2, westCluster)

			t.addCluster(northCluster)
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP3, northCluster)
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1},
				&lighthousev1.ClusterServiceInfo{ClusterID: westCluster, ServiceIP: serviceIP2},
				&lighthousev1.ClusterServiceInfo{ClusterID: northCluster, ServiceIP: serviceIP3})

			t.setupExpectDistribute()
			t.deleteService(serviceName1, serviceNS1, westCluster)
			Eventually(t.distributeCalled, 5).Should(Receive(), "Distribute was not called")
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1},
				&lighthousev1.ClusterServiceInfo{ClusterID: northCluster, ServiceIP: serviceIP3})

			t.setupExpectDistribute()
			t.deleteService(serviceName1, serviceNS1, eastCluster)
			Eventually(t.distributeCalled, 5).Should(Receive(), "Distribute was not called")
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: northCluster, ServiceIP: serviceIP3})
		})
	})

	When("a Service is deleted and the MultiClusterService delete initially fails", func() {
		It("should retry until it succeeds", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)

			// Simulate the first call to Delete fails and the second succeeds.
			gomock.InOrder(
				t.mockFederator.EXPECT().Delete(gomock.Any()).Return(errors.New("mock")),
				t.setupExpectDelete())
			t.deleteService(serviceName1, serviceNS1, eastCluster)

			Eventually(t.deleteCalled, 5).Should(Receive(), "Delete was not called")
			t.verifyDeletedMCService(serviceName1, serviceNS1)
		})
	})

	When("a deleted cluster Service is not present in the MultiClusterService", func() {
		It("should not delete the MultiClusterService", func() {
			t.createServiceAndVerifyDistribute(serviceName1, serviceNS1, serviceIP1, eastCluster)

			t.setupExpectDelete().MaxTimes(0)
			key, _ := cache.MetaNamespaceKeyFunc(newService(serviceName1, serviceNS1, serviceIP1))
			t.controller.remoteClusters[westCluster].queue.Add(key)

			Consistently(t.deleteCalled).ShouldNot(Receive(), "Delete was unexpectedly called")
			verifyMultiClusterService(t.checkCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})
}

func verifyMultiClusterService(mcs *lighthousev1.MultiClusterService, name, namespace string, serviceInfo ...*lighthousev1.ClusterServiceInfo) {
	Expect(mcs.Name).To(Equal(name))
	Expect(mcs.Namespace).To(Equal(namespace))

	infoMap := make(map[string]string)
	for _, info := range mcs.Spec.Items {
		infoMap[info.ClusterID] = info.ServiceIP
	}

	for _, info := range serviceInfo {
		ip, exists := infoMap[info.ClusterID]
		Expect(exists).To(BeTrue(), "MultiClusterService %#v missing ClusterServiceInfo for %q", mcs, info.ClusterID)
		Expect(ip).To(Equal(info.ServiceIP), "Unexpected ClusterServiceInfo ServiceIP for %q", info.ClusterID)
		delete(infoMap, info.ClusterID)
	}

	Expect(infoMap).To(BeEmpty(), "Unexpected ClusterServiceInfo items %s in MultiClusterService %#v", infoMap, mcs)
}

func newService(name, namespace, clusterIP string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: clusterIP,
		},
	}
}

func newTestDriver() *testDriver {
	t := &testDriver{
		mockCtrl: gomock.NewController(GinkgoT()),
		fakeClientsets: map[string]*fakeCoreClientSet.Clientset{eastCluster: fakeCoreClientSet.NewSimpleClientset(),
			westCluster: fakeCoreClientSet.NewSimpleClientset(), northCluster: fakeCoreClientSet.NewSimpleClientset()},
		capturedMCService: new(*lighthousev1.MultiClusterService),
	}

	t.mockFederator = mocks.NewMockFederator(t.mockCtrl)
	t.controller = New(t.mockFederator)

	t.controller.newClientset = func(clusterID string, c *rest.Config) (kubernetes.Interface, error) {
		cs, exists := t.fakeClientsets[clusterID]
		if !exists {
			return nil, fmt.Errorf("Unknown cluster %q", clusterID)
		}

		return cs, nil
	}

	t.addCluster(eastCluster)
	t.addCluster(westCluster)

	return t
}

func (t *testDriver) addCluster(clusterID string) {
	clientset := t.fakeClientsets[clusterID]
	watchReactor := &watchReactor{multiclusterServiceWatchStarted: make(chan bool, 1)}
	watchReactor.watch(clientset)

	t.controller.OnAdd(clusterID, &rest.Config{})

	Eventually(watchReactor.multiclusterServiceWatchStarted).Should(Receive())
	watchReactor.close()
}

func (t *testDriver) close() {
	t.closeChan(t.distributeCalled)
	t.closeChan(t.deleteCalled)
	t.mockCtrl.Finish()
}

func (t *testDriver) closeChan(c chan bool) {
	if c != nil {
		close(c)
	}
}

func (t *testDriver) createServiceAndVerifyDistribute(name, namespace, serviceIP, clusterID string) {
	t.setupExpectDistribute()
	t.createService(name, namespace, serviceIP, clusterID)
	Eventually(t.distributeCalled, 5).Should(Receive(), "Distribute was not called")
}

func (t *testDriver) createService(name, namespace, serviceIP, clusterID string) {
	_, err := t.fakeClientsets[clusterID].CoreV1().Services(namespace).Create(newService(name, namespace, serviceIP))
	Expect(err).To(Succeed())
}

func (t *testDriver) deleteService(name, namespace, clusterID string) {
	err := t.fakeClientsets[clusterID].CoreV1().Services(namespace).Delete(name, &metav1.DeleteOptions{})
	Expect(err).To(Succeed())
}

func (t *testDriver) setupExpectDistribute() *gomock.Call {
	t.closeChan(t.distributeCalled)
	t.distributeCalled = make(chan bool, 1)

	return t.mockFederator.EXPECT().Distribute(gomock.Any()).Return(nil).Do(func(m *lighthousev1.MultiClusterService) {
		*t.capturedMCService = m
		t.distributeCalled <- true
	})
}

func (t *testDriver) setupExpectDelete() *gomock.Call {
	t.closeChan(t.deleteCalled)
	t.deleteCalled = make(chan bool, 1)

	return t.mockFederator.EXPECT().Delete(gomock.Any()).Return(nil).Do(func(m *lighthousev1.MultiClusterService) {
		*t.capturedMCService = m
		t.deleteCalled <- true
	})
}

func (t *testDriver) checkCachedMCService(name, namespace string) *lighthousev1.MultiClusterService {
	mcs, exists := t.getCachedMCService(name, namespace)
	Expect(exists).To(BeTrue())
	return mcs
}

func (t *testDriver) getCachedMCService(name, namespace string) (*lighthousev1.MultiClusterService, bool) {
	t.controller.multiClusterServicesMutex.Lock()
	defer t.controller.multiClusterServicesMutex.Unlock()

	key, _ := cache.MetaNamespaceKeyFunc(newService(name, namespace, ""))
	mcs, exists := t.controller.multiClusterServices[key]
	return mcs, exists
}

func (t *testDriver) verifyDistributedMCService(name, namespace string, serviceInfo ...*lighthousev1.ClusterServiceInfo) {
	verifyMultiClusterService(*t.capturedMCService, name, namespace, serviceInfo...)
	verifyMultiClusterService(t.checkCachedMCService(name, namespace), name, namespace, serviceInfo...)
}

func (t *testDriver) verifyDeletedMCService(name, namespace string) {
	Expect((*t.capturedMCService).Name).To(Equal(name))
	Expect((*t.capturedMCService).Namespace).To(Equal(namespace))

	_, exists := t.getCachedMCService(name, namespace)
	Expect(exists).To(BeFalse())
}

func (t *testDriver) initMultiClusterService(name, namespace, clusterIP, clusterID string) {
	service := newService(name, namespace, clusterIP)
	key, err := cache.MetaNamespaceKeyFunc(service)
	Expect(err).To(Succeed())

	t.controller.multiClusterServicesMutex.Lock()
	defer t.controller.multiClusterServicesMutex.Unlock()
	t.controller.multiClusterServices[key] = &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.ObjectMeta.Name,
			Namespace: service.ObjectMeta.Namespace,
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				lighthousev1.ClusterServiceInfo{
					ClusterID: clusterID,
					ServiceIP: service.Spec.ClusterIP,
				},
			},
		},
	}
}

func newWatchReactor(c *LightHouseController) *watchReactor {
	fakeClientset := fakeCoreClientSet.NewSimpleClientset()
	w := &watchReactor{multiclusterServiceWatchStarted: make(chan bool, 1)}
	w.watch(fakeClientset)

	c.newClientset = func(clusterID string, c *rest.Config) (kubernetes.Interface, error) {
		return fakeClientset, nil
	}

	return w
}

func (w *watchReactor) watch(clientset *fakeCoreClientSet.Clientset) {
	clientset.PrependWatchReactor("*", func(action testing.Action) (handled bool, ret watch.Interface, err error) {
		if action.GetResource().Resource == "services" {
			w.multiclusterServiceWatchStarted <- true
		} else {
			fmt.Printf("Watch reactor received unexpected Resource: %s\n", action.GetResource().Resource)
		}
		return false, nil, nil
	})
}

func (w *watchReactor) reset() {
	w.close()
	w.multiclusterServiceWatchStarted = make(chan bool, 1)
}

func (w *watchReactor) close() {
	close(w.multiclusterServiceWatchStarted)
}
