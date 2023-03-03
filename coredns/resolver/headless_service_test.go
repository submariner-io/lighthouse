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
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("GetDNSRecords", func() {
	Describe("Headless Service", func() {
		When("a service is present in multiple clusters", testHeadlessServiceInMultipleClusters)
	})
})

func testHeadlessServiceInMultipleClusters() {
	t := newTestDriver()

	cluster1DNSRecord := resolver.DNSRecord{
		IP:          endpointIP1,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID1,
	}

	cluster2DNSRecord := resolver.DNSRecord{
		IP:          endpointIP2,
		Ports:       []mcsv1a1.ServicePort{port2},
		ClusterName: clusterID2,
	}

	cluster3DNSRecord1 := resolver.DNSRecord{
		IP:          endpointIP3,
		Ports:       []mcsv1a1.ServicePort{port3, port4},
		ClusterName: clusterID3,
		HostName:    hostName1,
	}

	cluster3DNSRecord2 := resolver.DNSRecord{
		IP:          endpointIP4,
		Ports:       []mcsv1a1.ServicePort{port3, port4},
		ClusterName: clusterID3,
		HostName:    hostName1,
	}

	cluster3DNSRecord3 := resolver.DNSRecord{
		IP:          endpointIP5,
		Ports:       []mcsv1a1.ServicePort{port3, port4},
		ClusterName: clusterID3,
	}

	cluster3DNSRecord4 := resolver.DNSRecord{
		IP:          endpointIP6,
		Ports:       []mcsv1a1.ServicePort{port3, port4},
		ClusterName: clusterID3,
		HostName:    hostName2,
	}

	JustBeforeEach(func() {
		t.resolver.PutServiceImport(newClusterHeadlessServiceImport(namespace1, service1, clusterID1))
		t.resolver.PutServiceImport(newClusterHeadlessServiceImport(namespace1, service1, clusterID2))
		t.resolver.PutServiceImport(newClusterHeadlessServiceImport(namespace1, service1, clusterID3))

		t.putEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port1}, discovery.Endpoint{
			Addresses: []string{endpointIP1},
		}))

		t.putEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port2}, discovery.Endpoint{
			Addresses: []string{endpointIP2},
		}))

		t.putEndpointSlice(newEndpointSlice(namespace1, service1, clusterID3, []mcsv1a1.ServicePort{port3, port4},
			discovery.Endpoint{
				Addresses:  []string{endpointIP3, endpointIP4},
				Hostname:   &hostName1,
				NodeName:   &nodeName1,
				Conditions: discovery.EndpointConditions{Ready: &ready},
			},
			discovery.Endpoint{
				Addresses: []string{endpointIP5},
				NodeName:  &nodeName2,
			},
			discovery.Endpoint{
				Addresses: []string{endpointIP6},
				Hostname:  &hostName2,
				NodeName:  &nodeName3,
			},
			discovery.Endpoint{
				Addresses:  []string{"1.2.3.4"},
				Conditions: discovery.EndpointConditions{Ready: &notReady},
			}))
	})

	Context("and no specific cluster is requested", func() {
		It("should return all the DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, "", "", true, cluster1DNSRecord,
				cluster2DNSRecord, cluster3DNSRecord1, cluster3DNSRecord2, cluster3DNSRecord3, cluster3DNSRecord4)
		})
	})

	Context("and a specific cluster is requested", func() {
		It("should return all its DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID3, "", true,
				cluster3DNSRecord1, cluster3DNSRecord2, cluster3DNSRecord3, cluster3DNSRecord4)
		})
	})

	Context("and a specific cluster and host name is requested", func() {
		It("should return its host name DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID3, hostName1, true,
				cluster3DNSRecord1, cluster3DNSRecord2)

			t.assertDNSRecordsFound(namespace1, service1, clusterID3, hostName2, true, cluster3DNSRecord4)
		})
	})

	Context("and one is on the local cluster", func() {
		BeforeEach(func() {
			t.clusterStatus.SetLocalClusterID(clusterID3)

			// If the local cluster EndpointSlice is created before the local K8s EndpointSlice, PutEndpointSlice should
			// return true to requeue.
			t.resolver.PutServiceImport(newClusterHeadlessServiceImport(namespace1, service1, clusterID3))
			Expect(t.resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID3, nil))).To(BeTrue())

			t.createEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local-" + service1,
					Namespace: namespace1,
					Labels: map[string]string{
						constants.KubernetesServiceName: service1,
					},
				},
				Endpoints: []discovery.Endpoint{
					{
						Addresses: []string{endpointIP3, endpointIP4},
						NodeName:  &nodeName1,
						TargetRef: &corev1.ObjectReference{
							Name: hostName1,
						},
					},
					{
						Addresses: []string{endpointIP5},
						NodeName:  &nodeName2,
					},
					{
						Addresses: []string{endpointIP6},
						NodeName:  &nodeName3,
						TargetRef: &corev1.ObjectReference{
							Name: hostName2,
						},
					},
				},
			})
		})

		It("should return all its DNS record", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID3, "", true,
				cluster3DNSRecord1, cluster3DNSRecord2, cluster3DNSRecord3, cluster3DNSRecord4)
		})
	})

	Context("and one becomes disconnected", func() {
		JustBeforeEach(func() {
			t.clusterStatus.DisconnectClusterID(clusterID3)
		})

		Context("and no specific cluster is requested", func() {
			It("should return the connected clusters' DNS records", func() {
				t.assertDNSRecordsFound(namespace1, service1, "", "", true,
					cluster1DNSRecord, cluster2DNSRecord)
			})
		})

		Context("and the disconnected cluster is requested", func() {
			It("should still return its DNS records", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID3, "", true,
					cluster3DNSRecord1, cluster3DNSRecord2, cluster3DNSRecord3, cluster3DNSRecord4)

				t.assertDNSRecordsFound(namespace1, service1, clusterID3, hostName1, true,
					cluster3DNSRecord1, cluster3DNSRecord2)
			})
		})
	})

	Context("and one is subsequently removed", func() {
		JustBeforeEach(func() {
			t.resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID3, nil))
		})

		Context("and no specific cluster is requested", func() {
			It("should return the remaining clusters' DNS records", func() {
				t.assertDNSRecordsFound(namespace1, service1, "", "", true,
					cluster1DNSRecord, cluster2DNSRecord)
			})
		})

		Context("and the removed cluster is requested", func() {
			It("should return no DNS records found", func() {
				t.assertDNSRecordsNotFound(namespace1, service1, clusterID3, "")
			})
		})
	})

	Context("and the endpoints for one cluster are updated", func() {
		expDNSRecord1 := resolver.DNSRecord{
			IP:          endpointIP4,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID3,
			HostName:    hostName1,
		}

		expDNSRecord2 := resolver.DNSRecord{
			IP:          endpointIP5,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID3,
			HostName:    hostName2,
		}

		expDNSRecord3 := resolver.DNSRecord{
			IP:          endpointIP6,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID3,
			HostName:    hostName2,
		}

		JustBeforeEach(func() {
			t.putEndpointSlice(newEndpointSlice(namespace1, service1, clusterID3, []mcsv1a1.ServicePort{port3},
				discovery.Endpoint{
					Addresses: []string{endpointIP4},
					Hostname:  &hostName1,
				},
				discovery.Endpoint{
					Addresses: []string{endpointIP5, endpointIP6},
					Hostname:  &hostName2,
				}))
		})

		It("should return the updated DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID3, "", true,
				expDNSRecord1, expDNSRecord2, expDNSRecord3)

			t.assertDNSRecordsFound(namespace1, service1, clusterID3, hostName1, true, expDNSRecord1)

			t.assertDNSRecordsFound(namespace1, service1, clusterID3, hostName2, true, expDNSRecord2, expDNSRecord3)
		})
	})

	Context("and a non-existent cluster is specified", func() {
		It("should return no DNS records found", func() {
			t.assertDNSRecordsNotFound(namespace1, service1, "non-existent", "")
		})
	})
}
