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
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Headless service syncing", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
		t.service.Spec.ClusterIP = corev1.ClusterIPNone
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		When("the Endpoints already exists", func() {
			It("should correctly sync a ServiceImport and EndpointSlice", func() {
				t.createEndpoints()
				t.createServiceExport()

				t.awaitHeadlessServiceImport()
				t.awaitEndpointSlice()
			})
		})

		When("the Endpoints doesn't initially exist", func() {
			It("should eventually sync a correct ServiceImport and EndpointSlice", func() {
				t.createServiceExport()
				t.awaitHeadlessServiceImport()

				t.createEndpoints()
				t.awaitUpdatedServiceImport("")
				t.awaitEndpointSlice()
			})
		})
	})

	When("the Endpoints for a service are updated", func() {
		It("should update the ServiceImport and EndpointSlice", func() {
			t.createEndpoints()
			t.createServiceExport()

			t.awaitHeadlessServiceImport()
			t.awaitEndpointSlice()

			t.endpoints.Subsets[0].Addresses = append(t.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.3"})
			t.updateEndpoints()
			t.awaitUpdatedServiceImport("")
			t.awaitUpdatedEndpointSlice(append(t.endpointIPs(), "10.253.6.1"))
		})
	})

	When("a ServiceExport is deleted", func() {
		It("should delete the ServiceImport and EndpointSlice", func() {
			t.createEndpoints()
			t.createServiceExport()
			t.awaitHeadlessServiceImport()
			t.awaitEndpointSlice()

			t.deleteServiceExport()
			t.awaitHeadlessServiceUnexported()
		})
	})
})
