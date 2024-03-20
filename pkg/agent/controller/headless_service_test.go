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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Headless Service export", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
		t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.cluster1.createService()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		Context("and the Service already exists", func() {
			BeforeEach(func() {
				t.cluster1.service.Spec.PublishNotReadyAddresses = true
			})

			It("should export the service", func() {
				t.cluster1.createServiceEndpointSlices()
				t.cluster1.createServiceExport()

				t.awaitHeadlessServiceExported(&t.cluster1)
			})
		})

		Context("and no backend service EndpointSlice initially exists", func() {
			It("should eventually export the EndpointSlice", func() {
				t.cluster1.createServiceExport()
				t.awaitAggregatedServiceImport(mcsv1a1.Headless, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)

				t.cluster1.createServiceEndpointSlices()
				t.awaitEndpointSlice(&t.cluster1)
			})
		})
	})

	When("the backend service EndpointSlice is updated", func() {
		It("should update the exported EndpointSlice", func() {
			t.cluster1.createServiceEndpointSlices()
			t.cluster1.createServiceExport()
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster1.serviceEndpointSlices[0].Endpoints = append(t.cluster1.serviceEndpointSlices[0].Endpoints,
				discovery.Endpoint{
					Addresses:  []string{"192.168.5.3"},
					Conditions: discovery.EndpointConditions{Ready: ptr.To(true)},
				})
			t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{t.cluster1.serviceEndpointSlices[0].Endpoints}

			t.cluster1.updateServiceEndpointSlices()
			t.awaitEndpointSlice(&t.cluster1)
		})
	})

	When("a ServiceExport is deleted", func() {
		It("should unexport the service", func() {
			t.cluster1.createServiceEndpointSlices()
			t.cluster1.createServiceExport()
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster1.deleteServiceExport()
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	Describe("in two clusters", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.cluster1.createServiceEndpointSlices()
			t.cluster1.createServiceExport()
		})

		It("should export the service in both clusters", func() {
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster2.createServiceEndpointSlices()
			t.cluster2.createService()
			t.cluster2.createServiceExport()

			t.awaitHeadlessServiceExported(&t.cluster1, &t.cluster2)

			t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			t.cluster2.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
		})
	})

	Describe("with multiple service EndpointSlices", func() {
		Specify("the exported EndpointSlices should be correctly updated as backend service EndpointSlices are updated", func() {
			By("Creating initial service EndpointSlice")

			t.cluster1.createServiceEndpointSlices()
			t.cluster1.createServiceExport()
			t.awaitHeadlessServiceExported(&t.cluster1)

			By("Creating another service EndpointSlice")

			t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices, discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:   fmt.Sprintf("%s-%s2", serviceName, clusterID1),
					Labels: t.cluster1.serviceEndpointSlices[0].Labels,
				},
				AddressType: discovery.AddressTypeIPv4,
				Endpoints: []discovery.Endpoint{
					{
						Addresses:  []string{epIP4},
						Conditions: discovery.EndpointConditions{Serving: ptr.To(true)},
					},
				},
			})
			t.cluster1.headlessEndpointAddresses = append(t.cluster1.headlessEndpointAddresses,
				t.cluster1.serviceEndpointSlices[1].Endpoints)

			t.cluster1.createServiceEndpointSlices()
			t.ensureEndpointSlice(&t.cluster1)

			By("Deleting service EndpointSlice")

			t.cluster1.deleteEndpointSlice(t.cluster1.serviceEndpointSlices[0].Name)

			t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices[:0], t.cluster1.serviceEndpointSlices[1:]...)
			t.cluster1.headlessEndpointAddresses = append(t.cluster1.headlessEndpointAddresses[:0],
				t.cluster1.headlessEndpointAddresses[1:]...)

			t.ensureEndpointSlice(&t.cluster1)
			t.ensureAggregatedServiceImport(mcsv1a1.Headless, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)
		})
	})
})
