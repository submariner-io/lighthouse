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
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
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
		Context("and the Service already exists", func() {
			It("should correctly sync a ServiceImport and update the ServiceExport status", func() {
				t.createService()
				t.createServiceExport()
				t.awaitServiceExported(t.service.Spec.ClusterIP)
			})
		})

		Context("and the Service doesn't initially exist", func() {
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

	When("the type of an exported Service is updated to an unsupported type", func() {
		It("should delete the ServiceImport and update the ServiceExport status", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP)

			t.service.Spec.Type = corev1.ServiceTypeNodePort
			t.updateService()

			t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))
			t.awaitServiceUnexported()
			t.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "NoServiceImport"))
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

	When("a ServiceExport is created for a Service whose type is unsupported", func() {
		BeforeEach(func() {
			t.service.Spec.Type = corev1.ServiceTypeNodePort
		})

		JustBeforeEach(func() {
			t.createService()
			t.createServiceExport()
		})

		It("should update the ServiceExport status appropriately and not sync a ServiceImport", func() {
			t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))
			t.awaitNoServiceImport(t.brokerServiceImportClient)
			t.ensureNoServiceExportCondition(constants.ServiceExportSynced)
		})

		Context("and is subsequently updated to a supported type", func() {
			It("should eventually sync a ServiceImport and update the ServiceExport status appropriately", func() {
				t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))

				t.service.Spec.Type = corev1.ServiceTypeClusterIP
				t.updateService()

				t.awaitServiceExported(t.service.Spec.ClusterIP)
			})
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

	When("two Services with the same name in different namespaces are exported", func() {
		It("should sync ServiceImport and EndpointSlice resources for each", func() {
			t.createEndpoints()
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP)
			t.awaitEndpointSlice()

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      t.service.Name,
					Namespace: "other-service-ns",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "10.253.9.2",
				},
			}

			serviceExport := &mcsv1a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
			}

			endpoints := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
			}

			test.CreateResource(dynamicEndpointsClient(t.cluster1.localDynClient, endpoints.Namespace), endpoints)
			test.CreateResource(t.cluster1.dynamicServiceClient().Namespace(service.Namespace), service)
			test.CreateResource(serviceExportClient(t.cluster1.localDynClient, service.Namespace), serviceExport)

			t.cluster1.awaitServiceImport(service, mcsv1a1.ClusterSetIP, service.Spec.ClusterIP)
			awaitServiceImport(t.brokerServiceImportClient, service, mcsv1a1.ClusterSetIP, service.Spec.ClusterIP)
			t.cluster2.awaitServiceImport(service, mcsv1a1.ClusterSetIP, service.Spec.ClusterIP)

			awaitEndpointSlice(endpointSliceClient(t.cluster1.localDynClient, service.Namespace), endpoints.Namespace, endpoints.Name)
			awaitEndpointSlice(t.brokerEndpointSliceClient, endpoints.Namespace, endpoints.Name)
			awaitEndpointSlice(endpointSliceClient(t.cluster2.localDynClient, service.Namespace), endpoints.Namespace, endpoints.Name)

			// Ensure the resources for the first Service weren't overwritten
			t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)
			t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)
			t.awaitEndpointSlice()
		})
	})
})
