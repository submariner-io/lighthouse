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
	"context"

	"github.com/submariner-io/lighthouse/coredns/constants"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Controller", func() {
	t := newTestDriver()

	When("a ClusterIP service EndpointSlice is created", func() {
		expDNSRecord := resolver.DNSRecord{
			IP:          serviceIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		}

		var endpointSlice *discovery.EndpointSlice

		JustBeforeEach(func() {
			endpointSlice = newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1)
			t.createEndpointSlice(endpointSlice)
		})

		Context("before the ServiceImport", func() {
			Specify("GetDNSRecords should eventually return its DNS record", func() {
				Consistently(func() bool {
					_, _, found := t.resolver.GetDNSRecords(namespace1, service1, "", "")
					return found
				}).Should(BeFalse())

				t.createServiceImport(newAggregatedServiceImport(namespace1, service1))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			})
		})

		Context("after a ServiceImport", func() {
			BeforeEach(func() {
				t.createServiceImport(newAggregatedServiceImport(namespace1, service1))
			})

			Context("and then the EndpointSlice is deleted", func() {
				Specify("GetDNSRecords should eventually return no DNS record", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)

					err := t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), endpointSlice.Name, metav1.DeleteOptions{})
					Expect(err).To(Succeed())

					t.awaitDNSRecordsFound(namespace1, service1, "", "", false)
				})
			})

			Context("and then the ServiceImport is deleted", func() {
				Specify("GetDNSRecords should eventually return not found", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)

					err := t.serviceImports.Namespace(namespace1).Delete(context.TODO(), service1, metav1.DeleteOptions{})
					Expect(err).To(Succeed())

					t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
				})
			})

			Context("and then the EndpointSlice is updated to unhealthy", func() {
				Specify("GetDNSRecords should eventually return no DNS record", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)

					endpointSlice.Endpoints[0].Conditions.Ready = ptr.To(false)
					test.UpdateResource(t.endpointSlices.Namespace(namespace1), endpointSlice)

					t.awaitDNSRecordsFound(namespace1, service1, "", "", false)
				})
			})
		})
	})

	When("there's multiple EndpointSlices for a headless service", func() {
		var epsName1, epsName2 string

		JustBeforeEach(func() {
			t.createServiceImport(newHeadlessAggregatedServiceImport(namespace1, service1))

			eps := newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port1},
				discovery.Endpoint{
					Addresses:  []string{endpointIP1},
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
				discovery.Endpoint{
					Addresses:  []string{endpointIP2},
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
			)
			epsName1 = eps.Name
			t.createEndpointSlice(eps)

			eps = newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port2},
				discovery.Endpoint{
					Addresses:  []string{endpointIP3},
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
				discovery.Endpoint{
					Addresses:  []string{endpointIP4},
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
			)
			epsName2 = eps.Name
			t.createEndpointSlice(eps)

			epsOnBroker := eps.DeepCopy()
			epsOnBroker.Namespace = test.RemoteNamespace
			t.createEndpointSlice(epsOnBroker)
		})

		Specify("GetDNSRecords should return their DNS record", func() {
			t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true,
				resolver.DNSRecord{
					IP:          endpointIP1,
					Ports:       []mcsv1a1.ServicePort{port1},
					ClusterName: clusterID1,
				},
				resolver.DNSRecord{
					IP:          endpointIP2,
					Ports:       []mcsv1a1.ServicePort{port1},
					ClusterName: clusterID1,
				},
				resolver.DNSRecord{
					IP:          endpointIP3,
					Ports:       []mcsv1a1.ServicePort{port2},
					ClusterName: clusterID1,
				},
				resolver.DNSRecord{
					IP:          endpointIP4,
					Ports:       []mcsv1a1.ServicePort{port2},
					ClusterName: clusterID1,
				})
		})

		Context("and one is deleted", func() {
			Specify("GetDNSRecords should return the remaining DNS records", func() {
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), epsName1, metav1.DeleteOptions{})).To(Succeed())

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true,
					resolver.DNSRecord{
						IP:          endpointIP3,
						Ports:       []mcsv1a1.ServicePort{port2},
						ClusterName: clusterID1,
					},
					resolver.DNSRecord{
						IP:          endpointIP4,
						Ports:       []mcsv1a1.ServicePort{port2},
						ClusterName: clusterID1,
					})
			})
		})

		Context("and both are deleted", func() {
			Specify("GetDNSRecords should return no DNS records", func() {
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), epsName1, metav1.DeleteOptions{})).To(Succeed())
				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), epsName2, metav1.DeleteOptions{})).To(Succeed())

				t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
			})
		})
	})

	When("an EndpointSlice is on the broker", func() {
		JustBeforeEach(func() {
			t.createServiceImport(newAggregatedServiceImport(namespace1, service1))
			t.createEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: test.RemoteNamespace,
					Labels: map[string]string{
						constants.MCSLabelSourceCluster: "test",
						mcsv1a1.LabelServiceName:        "test",
						constants.LabelSourceNamespace:  namespace1,
					},
				},
			})
		})

		It("should not process it", func() {
			Consistently(func() bool {
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
				return true
			}).Should(BeTrue())
		})
	})
})
