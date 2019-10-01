package controller

import (
	"errors"
	"fmt"
	"reflect"

	"k8s.io/client-go/tools/cache"

	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/watch"

	"k8s.io/klog"

	"k8s.io/client-go/rest"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/federate/mocks"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	core_v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeCoreClientSet "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/testing"
)

type watchReactor struct {
	multiclusterServiceWatchStarted chan bool
}

type eqMultiClusterService struct {
	expected *lighthousev1.MultiClusterService
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
		clusterWorkQueue := controller.remoteClusters["east"].queue

		controller.OnRemove("east")

		Expect(controller.remoteClusters).ShouldNot(HaveKey("east"))
		Expect(stopChan).To(BeClosed())
		Eventually(clusterWorkQueue.ShuttingDown).Should(BeTrue())
	}

	When("a cluster is added", func() {
		It("should start watches for the Services created", func() {
			testOnAdd("east")
		})
	})

	When("a cluster is removed", func() {
		It("should remove and close the clusterWatch", func() {
			testOnRemove("east")
		})
	})
}

func newMultiClusterService() *lighthousev1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservice1",
			Namespace: "testNS",
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				lighthousev1.ClusterServiceInfo{
					ClusterID: "east",
					ServiceIP: "192.168.56.20",
				},
			},
		},
	}
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
		Expect(ip).To(Equal(info.ServiceIP), "Unexpected ClusterServiceInfo ServiceIP")
		delete(infoMap, info.ClusterID)
	}

	Expect(infoMap).To(BeEmpty(), "Unexpected ClusterServiceInfo items %s in MultiClusterService %#v", infoMap, mcs)
}

func newService(name, namespace, clusterIP string) *core_v1.Service {
	return &core_v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: core_v1.ServiceSpec{
			ClusterIP: clusterIP,
		},
	}
}

func testResourceDistribution() {
	const serviceName1 = "serviceName1"
	const serviceNS1 = "serviceNS1"
	const serviceNS2 = "serviceNS2"
	const serviceIP1 = "192.168.56.1"
	const serviceIP2 = "192.168.56.2"
	const eastCluster = "east"
	const westCluster = "west"

	var (
		distributeCalled chan bool
		mockCtrl         *gomock.Controller
		mockFederator    *mocks.MockFederator
		controller       *LightHouseController
		fakeClientset    *fakeCoreClientSet.Clientset
		capturedMCS      **lighthousev1.MultiClusterService = new(*lighthousev1.MultiClusterService)
	)

	BeforeEach(func() {
		distributeCalled = make(chan bool, 1)
		mockCtrl = gomock.NewController(GinkgoT())
		mockFederator = mocks.NewMockFederator(mockCtrl)
		controller = New(mockFederator)
		fakeClientset = fakeCoreClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (kubernetes.Interface, error) {
			return fakeClientset, nil
		}

		controller.OnAdd(eastCluster, &rest.Config{})
	})

	AfterEach(func() {
		mockCtrl.Finish()
		mockCtrl = nil
	})

	createService1 := func() {
		_, err := fakeClientset.CoreV1().Services(serviceNS1).Create(newService(serviceName1, serviceNS1, serviceIP1))
		Expect(err).To(Succeed())
	}

	setupExpectDistribute := func() {
		mockFederator.EXPECT().Distribute(gomock.Any()).Return(nil).Do(func(m *lighthousev1.MultiClusterService) {
			*capturedMCS = m
			distributeCalled <- true
		})
	}

	getCachedMCService := func(name, namespace string) *lighthousev1.MultiClusterService {
		controller.remoteServicesMutex.Lock()
		defer controller.remoteServicesMutex.Unlock()

		key, _ := cache.MetaNamespaceKeyFunc(newService(name, namespace, ""))
		mcs, exists := controller.remoteServices[key]
		Expect(exists).To(BeTrue())
		return mcs
	}

	verifyCapturedMCService := func(name, namespace string, serviceInfo ...*lighthousev1.ClusterServiceInfo) {
		verifyMultiClusterService(*capturedMCS, name, namespace, serviceInfo...)
		verifyMultiClusterService(getCachedMCService(name, namespace), name, namespace, serviceInfo...)
	}

	initMultiClusterService := func(name, namespace, clusterIP, clusterID string) {
		service := newService(name, namespace, clusterIP)
		key, err := cache.MetaNamespaceKeyFunc(service)
		Expect(err).To(Succeed())

		controller.remoteServicesMutex.Lock()
		defer controller.remoteServicesMutex.Unlock()
		controller.remoteServices[key] = &lighthousev1.MultiClusterService{
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

	When("a Service is added with no existing MultiClusterService", func() {
		It("should create a new MultiClusterService and distribute it", func() {
			setupExpectDistribute()

			createService1()

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not called")
			verifyCapturedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("a Service with the same name and namespace as an existing MultiClusterService is added", func() {
		It("should append its ClusterServiceInfo to the existing MultiClusterService and distribute it", func() {
			initMultiClusterService(serviceName1, serviceNS1, serviceIP2, westCluster)
			setupExpectDistribute()

			createService1()

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not called")
			verifyCapturedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: westCluster, ServiceIP: serviceIP2},
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("a Service with an existing ClusterServiceInfo is re-added", func() {
		It("should not append its ClusterServiceInfo again and the MultiClusterService should not be distributed", func() {
			initMultiClusterService(serviceName1, serviceNS1, serviceIP1, eastCluster)
			mockFederator.EXPECT().Distribute(gomock.Any()).Return(nil).MaxTimes(0).Do(func(m *lighthousev1.MultiClusterService) {
				distributeCalled <- true
			})

			createService1()

			Consistently(distributeCalled).ShouldNot(Receive(), "Distribute was unexpectedly called")
			verifyMultiClusterService(getCachedMCService(serviceName1, serviceNS1), serviceName1, serviceNS1,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})

	When("a Service is added with the same name but different namespace as an existing MultiClusterService", func() {
		It("should create a new MultiClusterService and distribute it", func() {
			initMultiClusterService(serviceName1, serviceNS2, serviceIP2, eastCluster)
			setupExpectDistribute()

			createService1()

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not called")
			verifyCapturedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
			verifyMultiClusterService(getCachedMCService(serviceName1, serviceNS2), serviceName1, serviceNS2,
				&lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP2})
		})
	})

	When("a Service is added and Distribute initially fails", func() {
		It("should retry until it succeeds", func() {
			// Simulate the first call to Distribute fails and the second succeeds.
			gomock.InOrder(
				mockFederator.EXPECT().Distribute(gomock.Any()).Return(errors.New("mock")),
				mockFederator.EXPECT().Distribute(gomock.Any()).Return(nil).Do(func(m *lighthousev1.MultiClusterService) {
					*capturedMCS = m
					distributeCalled <- true
				}))

			createService1()

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not retried")
			verifyCapturedMCService(serviceName1, serviceNS1, &lighthousev1.ClusterServiceInfo{ClusterID: eastCluster, ServiceIP: serviceIP1})
		})
	})
}

func newWatchReactor(c *LightHouseController) *watchReactor {
	fakeClientset := fakeCoreClientSet.NewSimpleClientset()

	w := &watchReactor{
		multiclusterServiceWatchStarted: make(chan bool, 1),
	}

	fakeClientset.PrependWatchReactor("*", func(action testing.Action) (handled bool, ret watch.Interface, err error) {
		if action.GetResource().Resource == "services" {
			w.multiclusterServiceWatchStarted <- true
		} else {
			fmt.Printf("Watch reactor received unexpected Resource: %s\n", action.GetResource().Resource)
		}
		return false, nil, nil
	})

	c.newClientset = func(c *rest.Config) (kubernetes.Interface, error) {
		return fakeClientset, nil
	}

	return w
}

func (w *watchReactor) close() {
	close(w.multiclusterServiceWatchStarted)
}

func EqMultiClusterService(expected *lighthousev1.MultiClusterService) *eqMultiClusterService {
	return &eqMultiClusterService{expected}
}

func (m *eqMultiClusterService) Matches(x interface{}) bool {
	actual, ok := x.(*lighthousev1.MultiClusterService)
	if !ok {
		return false
	}
	return m.expected.Name == actual.Name && m.expected.Namespace == actual.Namespace && reflect.DeepEqual(m.expected.Spec, actual.Spec)
}

func (m *eqMultiClusterService) String() string {
	return fmt.Sprintf("is equal to %#v", m.expected)
}
