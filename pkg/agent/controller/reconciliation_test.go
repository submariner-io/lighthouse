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
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Reconciliation", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a local headless ServiceImport is stale on startup due to a missed ServiceExport delete event", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete the ServiceImport and EndpointSlice on reconciliation", func() {
			serviceImport := t.cluster1.awaitServiceImport(t.service, mcsv1a1.Headless, "")
			endpointSlice := t.cluster1.awaitEndpointSlice(t)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, endpointSlice)
			t.createService()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
			t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed Service delete event", func() {
		It("should delete the ServiceImport on reconciliation", func() {
			serviceImport := t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)
			t.createServiceExport()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
		})
	})

	When("a synced local ServiceImport is stale in the broker datastore on startup", func() {
		It("should delete it from the broker datastore on reconciliation", func() {
			serviceImport := t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerServiceImportClient, serviceImport)
			t.justBeforeEach()

			t.awaitServiceUnexported()
		})
	})

	When("a synced remote ServiceImport is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
			serviceImport := t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localServiceImportClient, serviceImport)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
		})
	})

	When("a synced local EndpointSlice is stale in the broker datastore on startup", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete it from the broker datastore on reconciliation", func() {
			endpointSlice := t.awaitBrokerEndpointSlice()

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerEndpointSliceClient, endpointSlice)
			t.justBeforeEach()

			t.awaitNoEndpointSlice(t.brokerEndpointSliceClient)
			t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)
		})
	})

	When("a synced remote EndpointSlice is stale in the local datastore on startup", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete it from the local datastore on reconciliation", func() {
			endpointSlice := t.cluster2.awaitEndpointSlice(t)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localEndpointSliceClient, endpointSlice)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)
		})
	})
})
