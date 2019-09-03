package controller

import (
	"errors"
	"fmt"
	"reflect"

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

func newService() *core_v1.Service {
	return &core_v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservice1",
			Namespace: "testNS",
		},
		Spec: core_v1.ServiceSpec{
			ClusterIP: "192.168.56.20",
		},
	}
}

func testResourceDistribution() {
	const clusterID = "east"
	const nameSpace = "testNS"

	var (
		service             *core_v1.Service
		multiClusterService *lighthousev1.MultiClusterService
		distributeCalled    chan bool
		mockCtrl            *gomock.Controller
		mockFederator       *mocks.MockFederator
		controller          *LightHouseController
		fakeClientset       *fakeCoreClientSet.Clientset
	)

	BeforeEach(func() {
		service = newService()
		multiClusterService = newMultiClusterService()
		distributeCalled = make(chan bool, 1)
		mockCtrl = gomock.NewController(GinkgoT())
		mockFederator = mocks.NewMockFederator(mockCtrl)
		controller = New(mockFederator)
		fakeClientset = fakeCoreClientSet.NewSimpleClientset()

		controller.newClientset = func(c *rest.Config) (kubernetes.Interface, error) {
			return fakeClientset, nil
		}

		controller.OnAdd(clusterID, &rest.Config{})
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	createService := func() error {
		_, err := fakeClientset.CoreV1().Services(nameSpace).Create(service)
		return err
	}

	When("a Cluster resource is added", func() {
		It("should distribute the resource", func() {
			var captured **lighthousev1.MultiClusterService = new(*lighthousev1.MultiClusterService)
			mockFederator.EXPECT().Distribute(EqMultiClusterService(multiClusterService)).Return(nil).Do(func(c *lighthousev1.MultiClusterService) {
				*captured = c
				distributeCalled <- true
			})

			Expect(createService()).To(Succeed())

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not called")
		})
	})

	When("a Cluster resource is added with a matching clusterID label", func() {
		It("should distribute the resource", func() {
			mockFederator.EXPECT().Distribute(EqMultiClusterService(multiClusterService)).Return(nil).Do(func(c *lighthousev1.MultiClusterService) {
				distributeCalled <- true
			})

			Expect(createService()).To(Succeed())

			Eventually(distributeCalled, 5).Should(Receive(), "Distribute was not called")
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
	return m.expected.Name == actual.Name && reflect.DeepEqual(m.expected.Spec, actual.Spec)
}

func (m *eqMultiClusterService) String() string {
	return fmt.Sprintf("is equal to %#v", m.expected)
}
