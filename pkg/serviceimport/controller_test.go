/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package serviceimport_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsClientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	fakeMCSClientSet "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned/fake"
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
		fakeClientSet mcsClientset.Interface
		store         *fakeStore
	)

	BeforeEach(func() {
		store = &fakeStore{
			put:    make(chan *mcsv1a1.ServiceImport, 10),
			remove: make(chan *mcsv1a1.ServiceImport, 10),
		}

		serviceImport = newServiceImport(namespace1, service1, serviceIP, clusterID)
		controller = serviceimport.NewController(store)
		fakeClientSet = fakeMCSClientSet.NewSimpleClientset()

		controller.NewClientset = func(c *rest.Config) (mcsClientset.Interface, error) {
			return fakeClientSet, nil
		}

		Expect(controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		controller.Stop()
	})

	createService := func(serviceImport *mcsv1a1.ServiceImport) error {
		_, err := fakeClientSet.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Create(
			context.TODO(), serviceImport, metav1.CreateOptions{})
		return err
	}

	updateService := func(serviceImport *mcsv1a1.ServiceImport) error {
		_, err := fakeClientSet.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Update(
			context.TODO(), serviceImport, metav1.UpdateOptions{})
		return err
	}

	deleteService := func(serviceImport *mcsv1a1.ServiceImport) error {
		err := fakeClientSet.MulticlusterV1alpha1().ServiceImports(serviceImport.Namespace).Delete(
			context.TODO(), serviceImport.Name, metav1.DeleteOptions{})
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

// nolint:unparam // `name` always receives `service1'.
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
				lhconstants.LighthouseLabelSourceCluster: clusterID,
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
