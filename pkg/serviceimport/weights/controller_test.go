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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	weightMapName = lhconstants.SubmarinerWeightMapKey

	ns1            = "test"
	localCluster   = "local"   // local
	remoteCluster1 = "remote1" // remote
	remoteCluster2 = "remote2" // remote
	serviceName    = "test-service"
)

var _ = Describe("ServiceImportWeights controller", func() {
	t := newWeightMapTestDiver()

	When("no weight is defined", func() {
		It("should return default weight of 1", func() {
			t.validateWeightNotAvailable(serviceName, "non-existing-ns", remoteCluster1)
			t.validateWeightNotAvailable("non-existing-svc", ns1, remoteCluster1)
			t.validateWeightNotAvailable(serviceName, ns1, "non-existing-cluster")
		})
	})

	When("a ServiceImportWeightMap is added", func() {
		BeforeEach(func() {
			t.createWeightsMap(weightMapName)
		})
		It("should return the relevant weight for the query", func() {
			t.validateWeightAvailable(serviceName, ns1, remoteCluster1, 8)
		})
		Context("and then removed", func() {
			It("should return default weight of 1", func() {
				t.deleteWeightsMap(weightMapName)
				t.validateWeightNotAvailable(serviceName, ns1, remoteCluster1)
			})
		})
		Context("and then updated", func() {
			It("should return the update weight", func() {
				t.updateWeightsMap(weightMapName, serviceName, ns1, remoteCluster1, 15)
				t.validateWeightAvailable(serviceName, ns1, remoteCluster1, 15)
			})
		})
	})
})

type weightMapTestDriver struct {
	controller *weights.Controller
	kubeClient lighthouseClientset.Interface
}

func newWeightMapTestDiver() *weightMapTestDriver {
	t := &weightMapTestDriver{}

	BeforeEach(func() {
		t.kubeClient = fakeClientSet.NewSimpleClientset()
		t.controller = weights.NewController(localCluster)
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

func createDefaultWeightMap(name string) *lighthousev1a1.ServiceImportWeightMap {
	return &lighthousev1a1.ServiceImportWeightMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: lighthousev1a1.ServiceImportWeightMapSpec{

			SourceClusterWeightMap: map[string]*lighthousev1a1.ClusterWeightMap{
				localCluster: {
					NamespaceWeightMap: map[string]*lighthousev1a1.NamespaceWeightMap{
						ns1: {
							ServiceWeightMap: map[string]*lighthousev1a1.ServiceWeightMap{
								serviceName: {
									TargetClusterWeightMap: map[string]int64{
										remoteCluster1: 8,
										remoteCluster2: 2,
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

func (n *weightMapTestDriver) updateWeightsMap(name, service, namespace, inCluster string, weight int64) {
	weightMapObj := createDefaultWeightMap(name)
	weightMapObj.
		Spec.
		SourceClusterWeightMap[localCluster].
		NamespaceWeightMap[namespace].
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

func (n *weightMapTestDriver) validateWeightAvailable(service, namesapce, inCluster string, weight int64) {
	Eventually(func() int64 {
		return t.controller.GetWeightFor(service, namesapce, inCluster)
	}, 5).Should(Equal(weight))
}

func (n *weightMapTestDriver) validateWeightNotAvailable(service, namesapce, inCluster string) {
	Eventually(func() int64 {
		return t.controller.GetWeightFor(service, namesapce, inCluster)
	}, 5).Should(Equal(int64(1)))
}
