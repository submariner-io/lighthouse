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
package weights_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev1a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	fakeClientSet "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/fake"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/serviceimport/weights"
	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

const (
	weightMapName = lhconstants.SubmarinerWeightMapKey

	ns1      = "ns1"
	cluster1 = "cluster1" // local
	cluster2 = "cluster2" // remote
	cluster3 = "cluster3" // remote
	scv1     = "scv1"
)

type weightMapTestDriver struct {
	controller *weights.Controller
	kubeClient lighthouseClientset.Interface
}

func createDefaultWeightMap(name string) *lighthousev1a1.ServiceImportWeightMap {
	return &lighthousev1a1.ServiceImportWeightMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: lighthousev1a1.ServiceImportWeightMapSpec{

			SourceClusterWeightMap: map[string]*lighthousev1a1.ClusterWeightMap{
				cluster1: {
					NamespaceWeightMap: map[string]*lighthousev1a1.NamespaceWeightMap{
						ns1: {
							ServiceWeightMap: map[string]*lighthousev1a1.ServiceWeightMap{
								scv1: {
									TargetClusterWeightMap: map[string]int64{
										cluster2: 8,
										cluster3: 2,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (n *weightMapTestDriver) updateWeightsMap(name, service, namesapce, inCluster string, weight int64) {
	weightMapObj := createDefaultWeightMap(name)
	weightMapObj.
		Spec.
		SourceClusterWeightMap[cluster1].
		NamespaceWeightMap[namesapce].
		ServiceWeightMap[service].
		TargetClusterWeightMap[inCluster] = weight

	_, err := n.kubeClient.LighthouseV1alpha1().
		ServiceImportWeightMaps(metav1.NamespaceAll).
		Update(context.TODO(), weightMapObj, metav1.UpdateOptions{})
	Expect(err).To(Succeed())
}

func (n *weightMapTestDriver) createWeightsMap(name string) {
	weightMapObj := createDefaultWeightMap(name)

	_, err := n.kubeClient.LighthouseV1alpha1().
		ServiceImportWeightMaps(metav1.NamespaceAll).
		Create(context.TODO(), weightMapObj, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (n *weightMapTestDriver) deleteWeightsMap(name string) {
	err := n.kubeClient.LighthouseV1alpha1().ServiceImportWeightMaps(metav1.NamespaceAll).Delete(context.TODO(), name, metav1.DeleteOptions{})
	Expect(err).To(Succeed())
}

func newWeightMapTestDiver() *weightMapTestDriver {
	t := &weightMapTestDriver{}

	BeforeEach(func() {
		t.kubeClient = fakeClientSet.NewSimpleClientset()
		t.controller = weights.NewController(cluster1)
		t.controller.NewClientset = func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return t.kubeClient, nil
		}

		Expect(t.controller.Start(&rest.Config{})).To(Succeed())
		Expect(v1.AddToScheme(scheme.Scheme)).To(Succeed())
	})

	AfterEach(func() {
		t.controller.Stop()
	})

	return t
}

var _ = Describe("ServiceImportWeights controller", func() {
	t := newWeightMapTestDiver()

	validateWeightAvailable := func(service, namesapce, inCluster string, weight int64) {
		Eventually(func() int64 {
			return t.controller.GetWeightFor(service, namesapce, inCluster)
		}, 5).Should(Equal(weight))
	}

	validateWeightNotAvailable := func(service, namesapce, inCluster string) {
		Eventually(func() int64 {
			return t.controller.GetWeightFor(service, namesapce, inCluster)
		}, 5).Should(Equal(int64(1)))
	}

	When("weigth does not exists for query", func() {
		It("should return default weight of 1", func() {
			validateWeightNotAvailable(scv1, "non-existing-ns", cluster2)
			validateWeightNotAvailable("non-existing-svc", ns1, cluster2)
			validateWeightNotAvailable(scv1, ns1, "non-existing-cluster")
		})
	})

	When("weight map is added", func() {
		BeforeEach(func() {
			t.createWeightsMap(weightMapName)
		})
		It("should return the relevant weight for the query", func() {
			validateWeightAvailable(scv1, ns1, cluster2, 8)
		})
		When("weigth map is removed", func() {
			It("should return default weight of 1", func() {
				t.deleteWeightsMap(weightMapName)
				validateWeightNotAvailable(scv1, ns1, cluster2)
			})
		})
		When("weigth map is updated", func() {
			It("should return the update weight", func() {
				t.updateWeightsMap(weightMapName, scv1, ns1, cluster2, 15)
				validateWeightAvailable(scv1, ns1, cluster2, 15)
			})
		})
	})
})
