package serviceimport

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

var _ = Describe("ServiceImport controller", func() {
	Describe("ServiceImport lifecycle notifications", testLifecycleNotifications)
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
		store         *fakeStore
	)

	BeforeEach(func() {
		store = &fakeStore{
			put:    make(chan *lighthousev2a1.ServiceImport, 10),
			remove: make(chan *lighthousev2a1.ServiceImport, 10),
		}

		serviceImport = newServiceImport(namespace1, service1, serviceIP, clusterID)
		controller = NewController(store)
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
		store.verifyPut(serviceImport)
	}

	testOnUpdate := func(serviceImport *lighthousev2a1.ServiceImport) {
		Expect(updateService(serviceImport)).To(Succeed())
		store.verifyPut(serviceImport)
	}

	testOnRemove := func(serviceImport *lighthousev2a1.ServiceImport) {
		testOnAdd(serviceImport)

		Expect(deleteService(serviceImport)).To(Succeed())
		store.verifyRemove(serviceImport)
	}

	testOnDoubleAdd := func(first *lighthousev2a1.ServiceImport, second *lighthousev2a1.ServiceImport) {
		Expect(createService(first)).To(Succeed())
		Expect(createService(second)).To(Succeed())

		store.verifyPut(first)
		store.verifyPut(second)
	}

	When("a ServiceImport is added", func() {
		It("it should be added to the ServiceImport store", func() {
			testOnAdd(serviceImport)
		})
	})

	When("a ServiceImport is updated", func() {
		It("it should be updated in the ServiceImport store", func() {
			testOnAdd(serviceImport)
			testOnUpdate(newServiceImport(namespace1, service1, serviceIP2, clusterID))
		})
	})

	When("the same ServiceImport is added in another cluster", func() {
		It("both should be added to the ServiceImport store", func() {
			testOnDoubleAdd(serviceImport, newServiceImport(namespace1, service1, serviceIP2, clusterID2))
		})
	})

	When("a ServiceImport is deleted", func() {
		It("it should be removed from the ServiceImport store", func() {
			testOnRemove(serviceImport)
		})
	})
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
		Spec: lighthousev2a1.ServiceImportSpec{
			Type: lighthousev2a1.SuperclusterIP,
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

type fakeStore struct {
	put    chan *lighthousev2a1.ServiceImport
	remove chan *lighthousev2a1.ServiceImport
}

func (f *fakeStore) Put(si *lighthousev2a1.ServiceImport) {
	f.put <- si
}

func (f *fakeStore) Remove(si *lighthousev2a1.ServiceImport) {
	f.remove <- si
}

func (f *fakeStore) verifyPut(expected *lighthousev2a1.ServiceImport) {
	Eventually(f.put, 5).Should(Receive(Equal(expected)), "Put was not called")
}

func (f *fakeStore) verifyRemove(expected *lighthousev2a1.ServiceImport) {
	Eventually(f.remove, 5).Should(Receive(Equal(expected)), "Remove was not called")
}
