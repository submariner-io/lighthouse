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
	"github.com/submariner-io/lighthouse/pkg/serviceimport/weights"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	weightMapName = "weights"
	ns1           = "ns1"
	cluster1      = "cluster1" // local
	scv1          = "scv1"
)

type weightMapTestDriver struct {
	controller *weights.Controller
	kubeClient lighthouseClientset.Interface
}

func (n *weightMapTestDriver) createWeightsMap(name string) {
	weightMapObj := &lighthousev1a1.ServiceImportWeights{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: lighthousev1a1.ServiceImportWeightsSpec{

			SourceClusterWeighsMap: map[string]*lighthousev1a1.ClusterWeightMap{
				"cluster1": &lighthousev1a1.ClusterWeightMap{
					NamespaceWeightMap: map[string]*lighthousev1a1.NamespaceWeightMap{
						"ns1": &lighthousev1a1.NamespaceWeightMap{
							ServiceWeightMap: map[string]*lighthousev1a1.ServiceWeightMap{
								"scv1": &lighthousev1a1.ServiceWeightMap{
									TargetClusterWeightMap: map[string]int64{
										"cluster2": 8,
										"cluster3": 2,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := n.kubeClient.LighthouseV1alpha1().ServiceImportWeights().Create(context.TODO(), weightMapObj, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (n *weightMapTestDriver) deleteNWeightsMap(name string) {
	err := n.kubeClient.LighthouseV1alpha1().ServiceImportWeights().Delete(context.TODO(), name, metav1.DeleteOptions{})
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
	})

	AfterEach(func() {
		t.controller.Stop()
	})

	return t
}

var _ = Describe("Namespace controller", func() {
	t := newWeightMapTestDiver()

	validateWeightAvailable := func(service, namesapce, inCluster string, weight int64) {
		Eventually(func() bool {
			return t.controller.GetWeightFor(service, namesapce, inCluster)
		}, 5).Should(EqualTo(weight))
	}

	validateWeightNotAvailable := func(service, namesapce, inCluster string) {
		Eventually(func() bool {
			return t.controller.LocalNamespacesContains(service, namesapce, inCluster)
		}, 5).Should(EqualTo(1))
	}

	When("namespace does not exists", func() {
		It("should return false", func() {
			validateNotAvailable("not-exsisting-namespace")
		})
	})

	When("namespace is added", func() {
		BeforeEach(func() {
			t.createWeightsMap(weightMapName)
		})
		It("should return true", func() {
			validateAvailable(namespace1)
		})
		When("namespace is removed", func() {
			It("should return false", func() {
				t.de(namespace1)
				validateNotAvailable(namespace1)
			})
		})
	})

})
