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

package controller_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Globalnet enabled", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
		t.cluster1.agentSpec.GlobalnetEnabled = true
		t.cluster2.agentSpec.GlobalnetEnabled = true
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.cluster1.createEndpoints()
		t.cluster1.createService()
		t.cluster1.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ClusterIP Service is exported", func() {
		var ingressIP *unstructured.Unstructured

		BeforeEach(func() {
			ingressIP = t.cluster1.newGlobalIngressIP(t.cluster1.service.Name, globalIP1)
			t.cluster1.serviceIP = globalIP1
		})

		Context("and it has a global IP", func() {
			Context("via a GlobalIngressIP", func() {
				BeforeEach(func() {
					t.cluster1.createGlobalIngressIP(ingressIP)
				})

				It("should export the service with the global IP", func() {
					t.awaitNonHeadlessServiceExported(&t.cluster1)
				})
			})
		})

		Context("and it does not initially have a global IP", func() {
			Context("due to missing GlobalIngressIP", func() {
				It("should update the ServiceExport status appropriately and eventually export the service", func() {
					t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					t.cluster1.createGlobalIngressIP(ingressIP)
					t.awaitNonHeadlessServiceExported(&t.cluster1)
				})
			})

			Context("due to no AllocatedIP in the GlobalIngressIP", func() {
				BeforeEach(func() {
					setIngressAllocatedIP(ingressIP, "")
					setIngressIPConditions(ingressIP)
					t.cluster1.createGlobalIngressIP(ingressIP)
				})

				It("should update the ServiceExport status appropriately and eventually export the service", func() {
					t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					setIngressAllocatedIP(ingressIP, globalIP1)
					test.UpdateResource(t.cluster1.localIngressIPClient, ingressIP)
					t.awaitNonHeadlessServiceExported(&t.cluster1)
				})
			})
		})

		Context("and the GlobalIngressIP has a status condition and no AllocatedIP", func() {
			condition := metav1.Condition{
				Type:    "Allocated",
				Status:  metav1.ConditionFalse,
				Reason:  "IPPoolAllocationFailed",
				Message: "IPPool is exhausted",
			}

			BeforeEach(func() {
				setIngressAllocatedIP(ingressIP, "")
				setIngressIPConditions(ingressIP, condition)
				t.cluster1.createGlobalIngressIP(ingressIP)
			})

			It("should update the ServiceExport status with the condition details", func() {
				c := newServiceExportValidCondition(corev1.ConditionFalse, condition.Reason)
				c.Message = &condition.Message
				t.cluster1.awaitServiceExportCondition(c)
			})
		})
	})

	When("a Headless Service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

			t.cluster1.endpointSliceAddresses[0].Addresses = []string{globalIP1}
			t.cluster1.endpointSliceAddresses[1].Addresses = []string{globalIP2}
		})

		Context("and it has global IPs for all endpoint addresses", func() {
			BeforeEach(func() {
				t.cluster1.createGlobalIngressIP(t.cluster1.newHeadlessGlobalIngressIP("one", globalIP1))
				t.cluster1.createGlobalIngressIP(t.cluster1.newHeadlessGlobalIngressIP("two", globalIP2))
			})

			It("should export the service with the global IPs", func() {
				t.awaitHeadlessServiceExported(&t.cluster1)
			})
		})

		Context("and it initially does not have a global IP for all endpoint addresses", func() {
			It("should eventually export the service with the global IPs", func() {
				Consistently(func() interface{} {
					return findEndpointSlice(t.cluster1.localEndpointSliceClient, t.cluster1.endpoints.Namespace,
						t.cluster1.endpoints.Name, t.cluster1.clusterID)
				}).Should(BeNil(), "Unexpected EndpointSlice")

				t.cluster1.createGlobalIngressIP(t.cluster1.newHeadlessGlobalIngressIP("one", globalIP1))

				Consistently(func() interface{} {
					return findEndpointSlice(t.cluster1.localEndpointSliceClient, t.cluster1.endpoints.Namespace,
						t.cluster1.endpoints.Name, t.cluster1.clusterID)
				}).Should(BeNil(), "Unexpected EndpointSlice")

				t.cluster1.createGlobalIngressIP(t.cluster1.newHeadlessGlobalIngressIP("two", globalIP2))

				t.awaitEndpointSlice(&t.cluster1)
			})
		})
	})
})
