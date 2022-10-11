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
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("ServiceImport syncing", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		When("the Service already exists", func() {
			It("should correctly sync a ServiceImport and update the ServiceExport status", func() {
				t.createService()
				t.createServiceExport()
				t.awaitServiceExported(t.service.Spec.ClusterIP)
			})
		})

		When("the Service doesn't initially exist", func() {
			It("should initially update the ServiceExport status to Initialized and eventually sync a ServiceImport", func() {
				t.createServiceExport()
				t.awaitServiceUnavailableStatus()

				t.createService()
				t.awaitServiceExported(t.service.Spec.ClusterIP)
			})
		})
	})

	When("a ServiceExport is deleted after a ServiceImport is synced", func() {
		It("should delete the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP)

			t.deleteServiceExport()
			t.awaitServiceUnexported()
		})
	})

	When("an exported Service is deleted and recreated while the ServiceExport still exists", func() {
		It("should delete and recreate the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP)

			t.deleteService()
			t.awaitServiceUnexported()
			t.awaitServiceUnavailableStatus()
			t.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "NoServiceImport"))

			t.createService()
			t.awaitServiceExported(t.service.Spec.ClusterIP)
		})
	})

	When("the ServiceImport sync initially fails", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportClient.PersistentFailOnCreate.Store("mock create error")
		})

		It("should eventually update the ServiceExport status to successfully synced", func() {
			t.createService()
			t.createServiceExport()

			t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionTrue, ""))
			t.ensureNoServiceExportCondition(constants.ServiceExportSynced)

			t.cluster1.localServiceImportClient.PersistentFailOnCreate.Store("")
			t.awaitServiceExported(t.service.Spec.ClusterIP)
		})
	})

	When("a ServiceExport is created for a Service whose type is other than ServiceTypeClusterIP", func() {
		BeforeEach(func() {
			t.service.Spec.Type = corev1.ServiceTypeNodePort
		})

		It("should update the ServiceExport status and not sync a ServiceImport", func() {
			t.createService()
			t.createServiceExport()

			t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))
			t.awaitNoServiceImport(t.brokerServiceImportClient)
		})
	})

	When("a Service has port information", func() {
		BeforeEach(func() {
			t.service.Spec.Ports = []corev1.ServicePort{
				{
					Name:     "eth0",
					Protocol: corev1.ProtocolTCP,
					Port:     123,
				},
				{
					Name:     "eth1",
					Protocol: corev1.ProtocolTCP,
					Port:     1234,
				},
			}
		})

		It("should set the appropriate port information in the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP)
		})
	})
})
