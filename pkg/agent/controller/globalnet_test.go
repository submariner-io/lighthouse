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
	"time"

	. "github.com/onsi/ginkgo"
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
		t.createService()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ClusterIP Service is exported", func() {
		var ingressIP *unstructured.Unstructured

		BeforeEach(func() {
			ingressIP = t.newGlobalIngressIP(t.service.Name, globalIP1)
		})

		Context("and it has a global IP", func() {
			Context("via a GlobalIngressIP", func() {
				BeforeEach(func() {
					t.createGlobalIngressIP(ingressIP)
				})

				It("should sync a ServiceImport with the global IP", func() {
					t.awaitServiceExported(globalIP1, 0)
				})
			})
		})

		Context("and it does not initially have a global IP", func() {
			Context("due to missing GlobalIngressIP", func() {
				It("should update the ServiceExport status appropriately and eventually sync a ServiceImport", func() {
					t.awaitServiceExportStatus(0, newServiceExportCondition(corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					t.createGlobalIngressIP(ingressIP)
					t.awaitServiceExported(globalIP1, 1)
				})
			})

			Context("due to no AllocatedIP in the GlobalIngressIP", func() {
				BeforeEach(func() {
					setIngressAllocatedIP(ingressIP, "")
					setIngressIPConditions(ingressIP)
					t.createGlobalIngressIP(ingressIP)
				})

				It("should update the ServiceExport status appropriately and eventually sync a ServiceImport", func() {
					t.awaitServiceExportStatus(0, newServiceExportCondition(corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					setIngressAllocatedIP(ingressIP, globalIP1)
					test.UpdateResource(t.cluster1.localIngressIPClient, ingressIP)
					t.awaitServiceExported(globalIP1, 1)
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
				t.createGlobalIngressIP(ingressIP)
			})

			It("should update the ServiceExport status with the condition details", func() {
				c := newServiceExportCondition(corev1.ConditionFalse, condition.Reason)
				c.Message = &condition.Message
				t.awaitServiceExportStatus(0, c)
			})
		})
	})

	When("a headless Service is exported", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		Context("and it has a global IP for all endpoint addresses", func() {
			BeforeEach(func() {
				t.createEndpointIngressIPs()
			})

			It("should sync a ServiceImport and EndpointSlice with the global IPs", func() {
				t.awaitHeadlessServiceImport()
				t.awaitEndpointSlice()
			})
		})

		Context("and it initially does not have a global IP for all endpoint addresses", func() {
			It("should eventually sync a ServiceImport and EndpointSlice with the global IPs", func() {
				time.Sleep(time.Millisecond * 300)
				t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)

				t.createEndpointIngressIPs()

				t.awaitHeadlessServiceImport()
				t.awaitEndpointSlice()
			})
		})
	})
})
