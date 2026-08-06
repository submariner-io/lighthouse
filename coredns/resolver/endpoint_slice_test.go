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

package resolver_test

import (
	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("PutEndpointSlices", func() {
	t := newTestDriver()

	When("the EndpointSlice is missing the required labels", func() {
		It("should not process it", func() {
			// Missing LabelServiceName
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace: "test",
						mcsv1a1.LabelSourceCluster:     "test",
					},
				},
			})

			// Missing LabelSourceNamespace
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						mcsv1a1.LabelSourceCluster: "test",
						mcsv1a1.LabelServiceName:   "test",
					},
				},
			})

			// Missing MCSLabelSourceCluster
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace: "test",
						mcsv1a1.LabelServiceName:       "test",
					},
				},
			})
		})
	})

	When("a ClusterIP EndpointSlice has a non-routable address", func() {
		It("should not add a DNS record", func() {
			t.resolver.PutServiceImport(newAggregatedServiceImport(namespace1, service1))

			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, "10.253.2.2", true, port1))
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, "127.0.0.1", true, port1))

			t.assertDNSRecordsFound(namespace1, service1, "", "", k8snet.IPv4, false)
		})
	})

	When("a headless EndpointSlice has non-routable addresses", func() {
		It("should not add DNS records for those addresses", func() {
			t.resolver.PutServiceImport(newHeadlessAggregatedServiceImport(namespace1, service1))

			// EndpointSlice with mix of routable and non-routable addresses
			t.putEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port1},
				discovery.Endpoint{
					Addresses:  []string{endpointIP1}, // Routable
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
				discovery.Endpoint{
					Addresses:  []string{"127.0.0.1"}, // Non-routable: loopback
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
				discovery.Endpoint{
					Addresses:  []string{"0.0.0.0"}, // Non-routable: unspecified
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
			))

			// Should only have the routable address
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv4, true,
				resolver.DNSRecord{
					IP:          endpointIP1,
					Ports:       []mcsv1a1.ServicePort{port1},
					ClusterName: clusterID1,
					HostName:    endpointHostname1,
				},
			)
		})
	})
})

var _ = Describe("RemoveEndpointSlice", func() {
	t := newTestDriver()

	When("the EndpointSlice is missing a required label", func() {
		It("should not process it", func() {
			t.resolver.RemoveEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace: "test",
						mcsv1a1.LabelSourceCluster:     "test",
					},
				},
			})
		})
	})

	When("the service information doesn't exist", func() {
		It("should not process it", func() {
			t.resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, nil))
		})
	})

	When("the cluster information doesn't exist", func() {
		It("should not process it", func() {
			t.resolver.PutServiceImport(newAggregatedServiceImport(namespace1, service1))

			t.resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, nil))
		})
	})

	When("the EndpointSlice is on the broker", func() {
		It("should not process it", func() {
			t.resolver.RemoveEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: test.RemoteNamespace,
					Labels: map[string]string{
						mcsv1a1.LabelSourceCluster:     "test",
						mcsv1a1.LabelServiceName:       "test",
						constants.LabelSourceNamespace: namespace1,
					},
				},
			})
		})
	})
})
