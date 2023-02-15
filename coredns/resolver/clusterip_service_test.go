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
	"github.com/submariner-io/lighthouse/coredns/resolver"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("GetDNSRecords", func() {
	Describe("ClusterIP Service", func() {
		When("a service is present in one cluster", testClusterIPServiceInOneCluster)
		When("a service is present in two clusters", testClusterIPServiceInTwoClusters)
		When("a service is present in three clusters", testClusterIPServiceInThreeClusters)

		testClusterIPServiceMisc()
	})
})

func testClusterIPServiceInOneCluster() {
	t := newTestDriver()

	expDNSRecord := resolver.DNSRecord{
		IP:          serviceIP1,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID1,
	}

	BeforeEach(func() {
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, expDNSRecord.IP, expDNSRecord.ClusterName,
			expDNSRecord.Ports...))

		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, expDNSRecord.ClusterName, expDNSRecord.IP, true))
	})

	Context("and no specific cluster is requested", func() {
		It("should consistently return its DNS record", func() {
			for i := 0; i < 5; i++ {
				t.assertDNSRecordsFound(namespace1, service1, "", "", false, expDNSRecord)
			}
		})
	})

	Context("and the cluster is requested", func() {
		It("should consistently return its DNS record", func() {
			for i := 0; i < 5; i++ {
				t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			}
		})
	})

	Context("and it becomes disconnected", func() {
		BeforeEach(func() {
			t.clusterStatus.ConnectedClusterIDs.RemoveAll()
		})

		It("should return no DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, "", "", false)
		})

		Context("and the cluster is requested", func() {
			It("should still return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			})
		})
	})

	Context("and it becomes unhealthy", func() {
		BeforeEach(func() {
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, false))
		})

		It("should return no DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, "", "", false)
		})

		Context("and the cluster is requested", func() {
			It("should still return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			})
		})
	})

	Context("and the service information is updated", func() {
		BeforeEach(func() {
			t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP2, clusterID1, port2))
		})

		It("should return the correct DNS record information", func() {
			t.assertDNSRecordsFound(namespace1, service1, "", "", false, resolver.DNSRecord{
				IP:          serviceIP2,
				Ports:       []mcsv1a1.ServicePort{port2},
				ClusterName: clusterID1,
			})
		})
	})

	Context("and a non-existent cluster is specified", func() {
		It("should return no DNS records found", func() {
			t.assertDNSRecordsNotFound(namespace1, service1, "non-existent", "")
		})
	})
}

func testClusterIPServiceInTwoClusters() {
	t := newTestDriver()

	BeforeEach(func() {
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP2, clusterID2, port1))

		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true))
		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, true))
	})

	Context("and no specific cluster is requested", func() {
		It("should consistently return the DNS records round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
		})
	})

	Context("and one is the local cluster", func() {
		BeforeEach(func() {
			t.clusterStatus.LocalClusterID.Store(clusterID1)
		})

		It("should consistently return its DNS record", func() {
			for i := 0; i < 10; i++ {
				Expect(t.getNonHeadlessDNSRecord(namespace1, service1, "").IP).To(Equal(serviceIP1))
			}
		})
	})

	Context("and one becomes disconnected", func() {
		expDNSRecord := resolver.DNSRecord{
			IP:          serviceIP2,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID2,
		}

		BeforeEach(func() {
			t.clusterStatus.ConnectedClusterIDs.Remove(clusterID1)
		})

		Context("and no specific cluster is requested", func() {
			It("should consistently return the DNS record of the connected cluster", func() {
				for i := 0; i < 10; i++ {
					t.assertDNSRecordsFound(namespace1, service1, "", "", false, expDNSRecord)
				}
			})
		})

		Context("and the disconnected cluster is requested", func() {
			It("should still return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, resolver.DNSRecord{
					IP:          serviceIP1,
					Ports:       []mcsv1a1.ServicePort{port1},
					ClusterName: clusterID1,
				})
			})
		})

		Context("and the connected cluster is requested", func() {
			It("should return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID2, "", false, expDNSRecord)
			})
		})
	})

	Context("and both become disconnected", func() {
		BeforeEach(func() {
			t.clusterStatus.ConnectedClusterIDs.RemoveAll()
		})

		It("should return no DNS records", func() {
			t.assertDNSRecordsFound(namespace1, service1, "", "", false)
		})
	})

	Context("and one becomes unhealthy", func() {
		expDNSRecord := resolver.DNSRecord{
			IP:          serviceIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		}

		BeforeEach(func() {
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, false))
		})

		Context("and no specific cluster is requested", func() {
			It("should consistently return the DNS record of the healthy cluster", func() {
				for i := 0; i < 10; i++ {
					t.assertDNSRecordsFound(namespace1, service1, "", "", false, expDNSRecord)
				}
			})
		})

		Context("and the unhealthy cluster is requested", func() {
			It("should still return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID2, "", false, resolver.DNSRecord{
					IP:          serviceIP2,
					Ports:       []mcsv1a1.ServicePort{port1},
					ClusterName: clusterID2,
				})
			})
		})

		Context("and the healthy cluster is requested", func() {
			It("should return its DNS record", func() {
				t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			})
		})
	})

	Context("and one is subsequently removed", func() {
		expDNSRecord := resolver.DNSRecord{
			IP:          serviceIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		}

		BeforeEach(func() {
			t.resolver.RemoveServiceImport(newClusterServiceImport(namespace1, service1, serviceIP2, clusterID2))
		})

		It("should consistently return the DNS record of the remaining cluster", func() {
			for i := 0; i < 10; i++ {
				t.assertDNSRecordsFound(namespace1, service1, "", "", false, expDNSRecord)
			}
		})
	})

	Context("and a non-existent local cluster is specified", func() {
		BeforeEach(func() {
			t.clusterStatus.LocalClusterID.Store("non-existent")
		})

		It("should consistently return the DNS records round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
		})
	})
}

func testClusterIPServiceInThreeClusters() {
	t := newTestDriver()

	BeforeEach(func() {
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP2, clusterID2, port1, port2))
		t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP3, clusterID3, port1))

		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true))
		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, true))
		t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID3, serviceIP3, true))
	})

	Context("and no specific cluster is requested", func() {
		It("should consistently return the DNS records round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2, serviceIP3)
		})

		It("should consistently return the merged service ports", func() {
			for i := 0; i < 10; i++ {
				Expect(t.getNonHeadlessDNSRecord(namespace1, service1, "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
			}
		})
	})

	Context("and a specific cluster is requested", func() {
		expDNSRecord := resolver.DNSRecord{
			IP:          serviceIP2,
			Ports:       []mcsv1a1.ServicePort{port1, port2},
			ClusterName: clusterID2,
		}

		It("should consistently return its DNS record", func() {
			for i := 0; i < 10; i++ {
				t.assertDNSRecordsFound(namespace1, service1, clusterID2, "", false, expDNSRecord)
			}
		})
	})

	Context("and one becomes disconnected", func() {
		BeforeEach(func() {
			t.clusterStatus.ConnectedClusterIDs.Remove(clusterID3)
		})

		It("should consistently return the connected clusters' DNS records round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
		})
	})

	Context("and one becomes unhealthy", func() {
		BeforeEach(func() {
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, false))
		})

		It("should consistently return the healthy clusters' DNS records round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP3)
		})

		Context("and subsequently healthy again", func() {
			It("should consistently return the all DNS records round-robin", func() {
				for i := 0; i < 5; i++ {
					Expect(t.getNonHeadlessDNSRecord(namespace1, service1, "").IP).To(Or(Equal(serviceIP1), Equal(serviceIP3)))
				}

				t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, true))

				t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2, serviceIP3)
			})
		})
	})

	Context("and one becomes disconnected and one becomes unhealthy", func() {
		BeforeEach(func() {
			t.clusterStatus.ConnectedClusterIDs.Remove(clusterID2)
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID3, serviceIP3, false))
		})

		It("should consistently return the remaining cluster's DNS record round-robin", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1)
		})
	})
}

func testClusterIPServiceMisc() {
	t := newTestDriver()

	When("a service exists in two namespaces", func() {
		BeforeEach(func() {
			t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1))
			t.resolver.PutServiceImport(newClusterServiceImport(namespace2, service1, serviceIP2, clusterID1))
		})

		It("should return the correct DNS record for each namespace", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, resolver.DNSRecord{
				IP:          serviceIP1,
				ClusterName: clusterID1,
			})

			t.assertDNSRecordsFound(namespace2, service1, clusterID1, "", false, resolver.DNSRecord{
				IP:          serviceIP2,
				ClusterName: clusterID1,
			})
		})
	})

	When("a per-cluster ServiceImport has the legacy annotations", func() {
		BeforeEach(func() {
			si := newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1)
			si.Labels = map[string]string{"lighthouse.submariner.io/sourceCluster": clusterID1}
			si.Annotations = map[string]string{"origin-name": service1, "origin-namespace": namespace1}
			t.resolver.PutServiceImport(si)
		})

		It("should correctly process it and return its DNS record", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, resolver.DNSRecord{
				IP:          serviceIP1,
				Ports:       []mcsv1a1.ServicePort{port1},
				ClusterName: clusterID1,
			})
		})
	})

	When("a cluster's EndpointSlice is created before its ServiceImport", func() {
		It("should correctly process them and return its DNS record", func() {
			t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP2, clusterID2))

			es := newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true)
			Expect(t.resolver.PutEndpointSlice(es)).To(BeTrue())

			t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)

			t.resolver.PutServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1))
			t.putEndpointSlice(es)

			t.assertDNSRecordsFound(namespace1, service1, "", "", false, resolver.DNSRecord{
				IP:          serviceIP1,
				ClusterName: clusterID1,
			})
		})
	})
}
