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

	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Globalnet enabled", func() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
		t.cluster1.agentSpec.GlobalnetEnabled = true
		t.cluster2.agentSpec.GlobalnetEnabled = true
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
		t.cluster1.createServiceEndpointSlices(ctx)
		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ClusterIP Service is exported", func() {
		var ingressIP *unstructured.Unstructured

		BeforeEach(func() {
			ingressIP = t.cluster1.newGlobalIngressIP(t.cluster1.service.Name, globalIP1)
		})

		JustBeforeEach(func() {
			t.cluster1.expectedClusterIPEndpoints[0].Addresses[0] = globalIP1
		})

		Context("and it has a global IP", func() {
			Context("via a GlobalIngressIP", func() {
				BeforeEach(func(ctx context.Context) {
					t.cluster1.createGlobalIngressIP(ctx, ingressIP)
				})

				It("should export the service with the global IP", func(ctx context.Context) {
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				})
			})
		})

		Context("and it does not initially have a global IP", func() {
			Context("due to missing GlobalIngressIP", func() {
				It("should update the ServiceExport status appropriately and eventually export the service", func(ctx context.Context) {
					t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
						controller.ServiceExportReasonGlobalIPUnavailable))

					By("Creating GlobalIngressIP")
					t.cluster1.createGlobalIngressIP(ctx, ingressIP)
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				})
			})

			Context("due to no AllocatedIP in the GlobalIngressIP", func() {
				BeforeEach(func(ctx context.Context) {
					setIngressAllocatedIP(ingressIP, "")
					setIngressIPConditions(ingressIP)
					t.cluster1.createGlobalIngressIP(ctx, ingressIP)
				})

				It("should update the ServiceExport status appropriately and eventually export the service", func(ctx context.Context) {
					t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
						controller.ServiceExportReasonGlobalIPUnavailable))

					By("Updating GlobalIngressIP")
					setIngressAllocatedIP(ingressIP, globalIP1)
					test.UpdateResource(ctx, t.cluster1.localIngressIPClient, ingressIP)
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
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

			BeforeEach(func(ctx context.Context) {
				setIngressAllocatedIP(ingressIP, "")
				setIngressIPConditions(ingressIP, condition)
				t.cluster1.createGlobalIngressIP(ctx, ingressIP)
			})

			It("should update the ServiceExport status with the condition details", func(ctx context.Context) {
				c := newServiceExportValidCondition(metav1.ConditionFalse, mcsv1a1.ServiceExportConditionReason(condition.Reason))
				c.Message = condition.Message
				t.cluster1.awaitServiceExportCondition(ctx, c)
			})
		})
	})

	When("a Headless Service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

			t.cluster1.headlessEndpointAddresses[0][0].Addresses = []string{globalIP1}
			t.cluster1.headlessEndpointAddresses[0][1].Addresses = []string{globalIP2}
			t.cluster1.headlessEndpointAddresses[0][2].Addresses = []string{globalIP3}
		})

		Context("and it has global IPs for all endpoint addresses", func() {
			BeforeEach(func(ctx context.Context) {
				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("one", globalIP1))
				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("two", globalIP2))
				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("not-ready", globalIP3))
			})

			It("should export the service with the global IPs", func(ctx context.Context) {
				t.awaitHeadlessServiceExported(ctx, &t.cluster1)
			})
		})

		Context("and it initially does not have a global IP for all endpoint addresses", func() {
			It("should eventually export the service with the global IPs", func(ctx context.Context) {
				t.cluster1.ensureNoEndpointSlice(ctx)

				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("one", globalIP1))

				t.cluster1.ensureNoEndpointSlice(ctx)

				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("two", globalIP2))
				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForPod("not-ready", globalIP3))

				t.awaitEndpointSlice(ctx, &t.cluster1)
			})
		})
	})

	When("a headless Service without a selector is exported", func() {
		BeforeEach(func(ctx context.Context) {
			t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

			// TargetRef is nil for all Endpoints of headless Service without selector and Hostname must be set.
			t.cluster1.serviceEndpointSlices[0].Endpoints = []discovery.Endpoint{
				{
					Addresses: []string{epIP1},
					Hostname:  &host1,
				},
				{
					Addresses: []string{epIP2},
					Hostname:  &host2,
				},
			}

			t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{
				{
					{
						Addresses: []string{globalIP1},
						Hostname:  &host1,
					},
					{
						Addresses: []string{globalIP2},
						Hostname:  &host2,
					},
				},
			}

			t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForEndpointIP("one", globalIP1, epIP1))
			t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForEndpointIP("two", globalIP2, epIP2))
		})

		It("should export the service with the global IPs", func(ctx context.Context) {
			t.awaitHeadlessServiceExported(ctx, &t.cluster1)
		})

		Context("and it initially does not have a global IP for all endpoint addresses", func() {
			BeforeEach(func() {
				t.cluster1.serviceEndpointSlices[0].Endpoints = append(t.cluster1.serviceEndpointSlices[0].Endpoints,
					discovery.Endpoint{
						Addresses: []string{epIP3},
						Hostname:  ptr.To("host3"),
					})

				t.cluster1.headlessEndpointAddresses[0] = append(t.cluster1.headlessEndpointAddresses[0],
					discovery.Endpoint{
						Addresses: []string{globalIP3},
						Hostname:  ptr.To("host3"),
					})
			})

			It("should eventually export the service with the global IPs", func(ctx context.Context) {
				t.cluster1.ensureNoEndpointSlice(ctx)
				t.cluster1.createGlobalIngressIP(ctx, t.cluster1.newHeadlessGlobalIngressIPForEndpointIP("three", globalIP3, epIP3))
				t.awaitEndpointSlice(ctx, &t.cluster1)
			})
		})
	})
})
