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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

var _ = Describe("Headless Service export", func() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
		t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
		t.cluster1.createService(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		Context("and the Service already exists", func() {
			BeforeEach(func() {
				t.cluster1.service.Spec.PublishNotReadyAddresses = true
			})

			It("should export the service", func(ctx context.Context) {
				t.cluster1.createServiceEndpointSlices(ctx)
				t.cluster1.createServiceExport(ctx)

				t.awaitHeadlessServiceExported(ctx, &t.cluster1)
			})
		})

		Context("and no backend service EndpointSlice initially exists", func() {
			It("should eventually export the EndpointSlice", func(ctx context.Context) {
				t.cluster1.createServiceExport(ctx)
				t.awaitAggregatedServiceImport(ctx, mcsv1b1.Headless, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)

				t.cluster1.createServiceEndpointSlices(ctx)
				t.awaitEndpointSlice(ctx, &t.cluster1)
			})
		})
	})

	When("the backend service EndpointSlice is updated", func() {
		It("should update the exported EndpointSlice", func(ctx context.Context) {
			t.cluster1.createServiceEndpointSlices(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster1.serviceEndpointSlices[0].Endpoints = append(t.cluster1.serviceEndpointSlices[0].Endpoints,
				discovery.Endpoint{
					Addresses:  []string{"192.168.5.3"},
					Conditions: discovery.EndpointConditions{Ready: new(true)},
				})
			t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{t.cluster1.serviceEndpointSlices[0].Endpoints}

			t.cluster1.updateServiceEndpointSlices(ctx)
			t.awaitEndpointSlice(ctx, &t.cluster1)
		})
	})

	When("a ServiceExport is deleted", func() {
		It("should unexport the service", func(ctx context.Context) {
			t.cluster1.createServiceEndpointSlices(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster1.deleteServiceExport(ctx)
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	Describe("in two clusters", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createServiceEndpointSlices(ctx)
			t.cluster1.createServiceExport(ctx)
		})

		It("should export the service in both clusters", func(ctx context.Context) {
			t.awaitHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster2.createServiceEndpointSlices(ctx)
			t.cluster2.createService(ctx)
			t.cluster2.createServiceExport(ctx)

			t.awaitHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
			t.cluster2.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
		})
	})

	Describe("with multiple service EndpointSlices", func() {
		Specify("the exported EndpointSlices should be correctly updated as backend service EndpointSlices are updated",
			func(ctx context.Context) {
				By("Creating initial service EndpointSlice")

				t.cluster1.createServiceEndpointSlices(ctx)
				t.cluster1.createServiceExport(ctx)
				t.awaitHeadlessServiceExported(ctx, &t.cluster1)

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
							Conditions: discovery.EndpointConditions{Serving: new(true)},
						},
					},
				})
				t.cluster1.headlessEndpointAddresses = append(t.cluster1.headlessEndpointAddresses,
					t.cluster1.serviceEndpointSlices[1].Endpoints)

				t.cluster1.createServiceEndpointSlices(ctx)
				t.ensureEndpointSlice(ctx, &t.cluster1)

				By("Deleting service EndpointSlice")

				t.cluster1.deleteEndpointSlice(ctx, t.cluster1.serviceEndpointSlices[0].Name)

				t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices[:0], t.cluster1.serviceEndpointSlices[1:]...)
				t.cluster1.headlessEndpointAddresses = append(t.cluster1.headlessEndpointAddresses[:0],
					t.cluster1.headlessEndpointAddresses[1:]...)

				t.ensureEndpointSlice(ctx, &t.cluster1)
				t.ensureAggregatedServiceImport(ctx, mcsv1b1.Headless, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)
			})
	})
})
