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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("Dual-stack", func() {
	const ipv6ServiceIP = "fc00:2001::6757"

	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()

		t.cluster1.createServiceEndpointSlices()
		t.cluster1.createService()
		t.cluster1.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a dual-stack ClusterIP service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
			t.cluster1.service.Spec.ClusterIPs = append(t.cluster1.service.Spec.ClusterIPs, ipv6ServiceIP)

			t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices, newIPv6ServiceEndpointSlice())
			t.cluster1.serviceEndpointSlices[1].Endpoints[0].Conditions = discovery.EndpointConditions{Ready: ptr.To(false)}
		})

		JustBeforeEach(func() {
			t.cluster1.expectedClusterIPEndpoints[1].Conditions = t.cluster1.serviceEndpointSlices[1].Endpoints[0].Conditions
		})

		Context("then unexported", func() {
			It("should create/delete separate EndpointSlices for IPv4 and IPv6", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1)

				By("Updating the IPv6 EndpointSlice")

				t.cluster1.serviceEndpointSlices[1].Endpoints[0].Conditions = discovery.EndpointConditions{Ready: ptr.To(true)}
				t.cluster1.expectedClusterIPEndpoints[1].Conditions = t.cluster1.serviceEndpointSlices[1].Endpoints[0].Conditions

				t.cluster1.updateServiceEndpointSlices()
				t.ensureEndpointSlice(&t.cluster1)

				By("Deleting the ServiceExport")

				t.cluster1.deleteServiceExport()
				t.awaitServiceUnexported(&t.cluster1)
			})
		})

		Context("with Globalnet enabled and an IPv4 global IP", func() {
			BeforeEach(func() {
				t.cluster1.agentSpec.GlobalnetEnabled = true
				t.cluster1.createGlobalIngressIP(t.cluster1.newGlobalIngressIP(t.cluster1.service.Name, globalIP1))
			})

			JustBeforeEach(func() {
				t.cluster1.expectedClusterIPEndpoints[0].Addresses[0] = globalIP1
			})

			It("should only retrieve the global IP for the IPv4 EndpointSlice", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1)
			})
		})
	})

	When("an IPv6 ClusterIP service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
			t.cluster1.service.Spec.ClusterIPs = []string{ipv6ServiceIP}
			t.cluster1.serviceEndpointSlices = []discovery.EndpointSlice{newIPv6ServiceEndpointSlice()}
		})

		It("should only create an IPv6 EndpointSlice", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})

		Context("with Globalnet enabled", func() {
			BeforeEach(func() {
				t.cluster1.agentSpec.GlobalnetEnabled = true
			})

			It("should not try to retrieve a global IP for the IPv6 EndpointSlice", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1)
			})
		})
	})

	When("a dual-stack headless service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
			t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

			t.cluster1.serviceEndpointSlices[0].Endpoints = []discovery.Endpoint{t.cluster1.serviceEndpointSlices[0].Endpoints[0]}
			t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices, newIPv6ServiceEndpointSlice())

			t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{
				t.cluster1.serviceEndpointSlices[0].Endpoints,
				t.cluster1.serviceEndpointSlices[1].Endpoints,
			}
		})

		Context("then unexported", func() {
			It("should create/delete separate EndpointSlices for IPv4 and IPv6", func() {
				t.awaitHeadlessServiceExported(&t.cluster1)

				By("Deleting the ServiceExport")

				t.cluster1.deleteServiceExport()
				t.awaitServiceUnexported(&t.cluster1)
			})
		})

		Context("with Globalnet enabled and an IPv4 global IP", func() {
			BeforeEach(func() {
				t.cluster1.agentSpec.GlobalnetEnabled = true
				t.cluster1.headlessEndpointAddresses[0][0].Addresses = []string{globalIP1}

				t.cluster1.createGlobalIngressIP(t.cluster1.newHeadlessGlobalIngressIPForPod("one", globalIP1))
			})

			It("should only retrieve the global IP for the IPv4 EndpointSlice", func() {
				t.awaitHeadlessServiceExported(&t.cluster1)
			})
		})
	})

	When("a IPv6 headless service is exported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
			t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

			t.cluster1.serviceEndpointSlices = []discovery.EndpointSlice{newIPv6ServiceEndpointSlice()}
			t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{t.cluster1.serviceEndpointSlices[0].Endpoints}
		})

		It("should only create an IPv6 EndpointSlice", func() {
			t.awaitHeadlessServiceExported(&t.cluster1)
		})
	})
})

func newIPv6ServiceEndpointSlice() discovery.EndpointSlice {
	return discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: serviceName + "-ipv6",
			Labels: map[string]string{
				discovery.LabelServiceName: serviceName,
			},
		},
		AddressType: discovery.AddressTypeIPv6,
		Endpoints: []discovery.Endpoint{
			{
				Addresses:  []string{"fd00:2001::1234"},
				Conditions: discovery.EndpointConditions{Ready: ptr.To(true)},
			},
		},
	}
}
