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
	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
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
		Context("and it has a global IP", func() {
			Context("via a GlobalIngressIP", func() {
				BeforeEach(func() {
					t.createGlobalIngressIP()
				})

				It("should sync a ServiceImport with the global IP", func() {
					t.awaitServiceExported(globalIP, 0)
				})
			})

			// TODO: Remove once we switch to globalnet V2
			Context("via an annotation", func() {
				BeforeEach(func() {
					t.service.SetAnnotations(map[string]string{"submariner.io/globalIp": globalIP})
				})

				It("should sync a ServiceImport with the global IP", func() {
					t.awaitServiceExported(globalIP, 0)
				})
			})
		})

		Context("and it does not initially have a global IP", func() {
			Context("due to missing GlobalIngressIP", func() {
				It("should update the ServiceExport status appropriately and eventually sync a ServiceImport", func() {
					t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
						corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					t.createGlobalIngressIP()
					t.awaitServiceExported(globalIP, 1)
				})
			})

			Context("due to no AllocatedIP in the GlobalIngressIP", func() {
				BeforeEach(func() {
					setIngressAllocatedIP(t.ingressIP, "")
					setIngressIPConditions(t.ingressIP)
					t.createGlobalIngressIP()
				})

				It("should update the ServiceExport status appropriately and eventually sync a ServiceImport", func() {
					t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
						corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

					setIngressAllocatedIP(t.ingressIP, globalIP)
					test.UpdateResource(t.cluster1.localIngressIPClient, t.ingressIP)
					t.awaitServiceExported(globalIP, 1)
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
				setIngressAllocatedIP(t.ingressIP, "")
				setIngressIPConditions(t.ingressIP, condition)
				t.createGlobalIngressIP()
			})

			It("should update the ServiceExport status with the condition details", func() {
				c := newServiceExportCondition(mcsv1a1.ServiceExportValid, corev1.ConditionFalse, condition.Reason)
				c.Message = &condition.Message
				t.awaitServiceExportStatus(0, c)
			})
		})
	})

	When("a headless Service is exported", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		It("should update the ServiceExport status and not sync a ServiceImport", func() {
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionFalse, "UnsupportedServiceType"))

			t.awaitNoServiceImport(t.brokerServiceImportClient)
		})
	})
})
