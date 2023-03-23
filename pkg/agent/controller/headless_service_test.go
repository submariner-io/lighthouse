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
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/utils/pointer"
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
			It("should export the service", func() {
				t.cluster1.createEndpoints()
				t.cluster1.createServiceExport()

				t.awaitHeadlessServiceExported(&t.cluster1)
			})
		})

		Context("and the Endpoints doesn't initially exist", func() {
			It("should eventually export the EndpointSlice", func() {
				t.cluster1.createServiceExport()
				t.awaitAggregatedServiceImport(mcsv1a1.Headless, t.cluster1.service.Name, t.cluster1.service.Namespace)

				t.cluster1.createEndpoints()
				t.awaitEndpointSlice(&t.cluster1)
			})
		})
	})

	When("the Endpoints for a service are updated", func() {
		It("should update the exported EndpointSlice", func() {
			t.cluster1.createEndpoints()
			t.cluster1.createServiceExport()
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster1.endpoints.Subsets[0].Addresses = append(t.cluster1.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.3"})
			t.cluster1.endpointSliceAddresses = append(t.cluster1.endpointSliceAddresses, discovery.Endpoint{
				Addresses:  []string{"192.168.5.3"},
				Conditions: discovery.EndpointConditions{Ready: pointer.Bool(true)},
			})

			t.cluster1.updateEndpoints()
			t.awaitEndpointSlice(&t.cluster1)
		})
	})

	When("a ServiceExport is deleted", func() {
		It("should unexport the service", func() {
			t.cluster1.createEndpoints()
			t.cluster1.createServiceExport()
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster1.deleteServiceExport()
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	Describe("Two clusters", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.cluster1.createEndpoints()
			t.cluster1.createServiceExport()
		})

		It("should export the service in both clusters", func() {
			t.awaitHeadlessServiceExported(&t.cluster1)

			t.cluster2.createEndpoints()
			t.cluster2.createService()
			t.cluster2.createServiceExport()

			t.awaitHeadlessServiceExported(&t.cluster1, &t.cluster2)

			t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			t.cluster2.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
		})
	})
})
