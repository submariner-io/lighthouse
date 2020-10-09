package serviceimport_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/mcs/client/clientset/versioned"
	fakeMCSClientSet "github.com/submariner-io/lighthouse/pkg/mcs/client/clientset/versioned/fake"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
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
		serviceImport *mcsv1a1.ServiceImport
		controller    *serviceimport.Controller
		fakeClientset mcsClientset.Interface
		store         *fakeStore
	)

	BeforeEach(func() {
		store = &fakeStore{
			put:    make(chan *mcsv1a1.ServiceImport, 10),
			remove: make(chan *mcsv1a1.ServiceImport, 10),
		}

		serviceImport = newServiceImport(namespace1, service1, serviceIP, clusterID)
		controller = serviceimport.NewController(store)
		fakeClientset = fakeMCSClientSet.NewSimpleClientset()

		controller.NewClientset = func(c *rest.Config) (mcsClientset.Interface, error) {
			return fakeClientset, nil
		}

		Expect(controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.Stop()
	})

	createService := func(serviceImport *mcsv1a1.ServiceImport) error {
		_, err := fakeClientset.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Create(serviceImport)
		return err
	}

	updateService := func(serviceImport *mcsv1a1.ServiceImport) error {
		_, err := fakeClientset.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Update(serviceImport)
		return err
	}

	deleteService := func(serviceImport *mcsv1a1.ServiceImport) error {
		err := fakeClientset.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Delete(serviceImport.Name, &metav1.DeleteOptions{})
		return err
	}

	testOnAdd := func(serviceImport *mcsv1a1.ServiceImport) {
		Expect(createService(serviceImport)).To(Succeed())
		store.verifyPut(serviceImport)
	}

	testOnUpdate := func(serviceImport *mcsv1a1.ServiceImport) {
		Expect(updateService(serviceImport)).To(Succeed())
		store.verifyPut(serviceImport)
	}

	testOnRemove := func(serviceImport *mcsv1a1.ServiceImport) {
		testOnAdd(serviceImport)

		Expect(deleteService(serviceImport)).To(Succeed())
		store.verifyRemove(serviceImport)
	}

	testOnDoubleAdd := func(first *mcsv1a1.ServiceImport, second *mcsv1a1.ServiceImport) {
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

func newServiceImport(namespace, name, serviceIP, clusterID string) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: namespace,
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
			Labels: map[string]string{
				lhconstants.LabelSourceCluster: clusterID,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type: mcsv1a1.ClusterSetIP,
			IPs:  []string{serviceIP},
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID,
				},
			},
		},
	}
}

type fakeStore struct {
	put    chan *mcsv1a1.ServiceImport
	remove chan *mcsv1a1.ServiceImport
}

func (f *fakeStore) Put(si *mcsv1a1.ServiceImport) {
	f.put <- si
}

func (f *fakeStore) Remove(si *mcsv1a1.ServiceImport) {
	f.remove <- si
}

func (f *fakeStore) verifyPut(expected *mcsv1a1.ServiceImport) {
	Eventually(f.put, 5).Should(Receive(Equal(expected)), "Put was not called")
}

func (f *fakeStore) verifyRemove(expected *mcsv1a1.ServiceImport) {
	Eventually(f.remove, 5).Should(Receive(Equal(expected)), "Remove was not called")
}
