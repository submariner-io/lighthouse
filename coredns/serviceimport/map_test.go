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

package serviceimport_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/serviceimport"
	corev1 "k8s.io/api/core/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("ServiceImport Map", func() {
	const (
		service1       = "service1"
		namespace1     = "namespace1"
		namespace2     = "namespace2"
		serviceIP1     = "192.168.56.21"
		serviceIP2     = "192.168.56.22"
		serviceIP3     = "192.168.56.23"
		localClusterID = "local"
		clusterID1     = "clusterID1"
		clusterID2     = "clusterID2"
		clusterID3     = "clusterID3"
	)

	port1 := mcsv1a1.ServicePort{
		Name:     "http",
		Protocol: corev1.ProtocolTCP,
		Port:     8080,
	}

	port2 := mcsv1a1.ServicePort{
		Name:     "voip",
		Protocol: corev1.ProtocolUDP,
		Port:     4567,
	}

	var (
		clusterStatusMap  map[string]bool
		serviceImportMap  *serviceimport.Map
		endpointStatusMap map[string]bool
	)

	BeforeEach(func() {
		clusterStatusMap = map[string]bool{clusterID1: true, clusterID2: true, clusterID3: true}
		serviceImportMap = serviceimport.NewMap(localClusterID)
		endpointStatusMap = map[string]bool{clusterID1: true, clusterID2: true, clusterID3: true}
	})

	checkCluster := func(id string) bool {
		return clusterStatusMap[id]
	}

	checkEndpoint := func(_, _, id string) bool {
		return endpointStatusMap[id]
	}

	expectIPsNotFound := func(ns, service, cluster, localCluster string) {
		_, found := serviceImportMap.GetIP(ns, service, cluster, localCluster, checkCluster, checkEndpoint)
		Expect(found).To(BeFalse())
	}

	getDNSRecordExpectFound := func(ns, name, cluster, localCluster string) *serviceimport.DNSRecord {
		dnsRecord, found := serviceImportMap.GetIP(ns, name, cluster, localCluster, checkCluster, checkEndpoint)
		Expect(found).To(BeTrue())

		return dnsRecord
	}

	getDNSRecord := func(ns, name, cluster, localCluster string) *serviceimport.DNSRecord {
		dnsRecord := getDNSRecordExpectFound(ns, name, cluster, localCluster)
		Expect(dnsRecord).ToNot(BeNil())

		return dnsRecord
	}

	getIPExpectFound := func(ns, name, cluster, localCluster string) string {
		dnsRecord := getDNSRecordExpectFound(ns, name, cluster, localCluster)
		if dnsRecord != nil {
			return dnsRecord.IP
		}

		return ""
	}

	getIP := func(ns, name string) string {
		ip := getIPExpectFound(ns, name, "", "")
		return ip
	}

	getClusterIP := func(ns, name, cluster string) string {
		ip := getIPExpectFound(ns, name, cluster, "")
		return ip
	}

	testRoundRobin := func(ns, service, cluster, localCluster string, serviceIPs []string) {
		contains := func(slice []string, str string) bool {
			for _, s := range slice {
				if s == str {
					return true
				}
			}

			return false
		}

		ipsCount := len(serviceIPs)
		rrIPs := make([]string, 0)

		for i := 0; i < ipsCount; i++ {
			ip := getIPExpectFound(ns, service, cluster, localCluster)
			rrIPs = append(rrIPs, ip)
			slice := rrIPs[0:i]
			Expect(contains(slice, ip)).To(BeFalse())
			Expect(contains(serviceIPs, ip)).To(BeTrue())
		}

		for i := 0; i < 5; i++ {
			for _, ip := range rrIPs {
				testIP := getIPExpectFound(ns, service, cluster, localCluster)
				Expect(testIP).To(Equal(ip))
			}
		}
	}

	When("a service is present in only one connected cluster", func() {
		BeforeEach(func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
		})

		It("should consistently return the IP of the connected cluster", func() {
			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(serviceIP1))
			}
		})

		It("should return the service ports of the connected cluster", func() {
			Expect(getDNSRecord(namespace1, service1, "", "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
		})

		When("any local cluster is specified", func() {
			It("should consistently return the IP of the connected cluster", func() {
				for i := 0; i < 10; i++ {
					Expect(getIPExpectFound(namespace1, service1, "", clusterID1)).To(Equal(serviceIP1))
					Expect(getIPExpectFound(namespace1, service1, "", clusterID2)).To(Equal(serviceIP1))
				}
			})
		})

		When("an invalid cluster is specified", func() {
			It("should consistently return not found regardless of local cluster", func() {
				for i := 0; i < 10; i++ {
					expectIPsNotFound(namespace1, service1, clusterID2, "")
					expectIPsNotFound(namespace1, service1, clusterID2, clusterID1)
					expectIPsNotFound(namespace1, service1, clusterID2, clusterID2)
				}
			})
		})

		When("the connected cluster is specified", func() {
			It("should consistently return its IP regardless of local cluster", func() {
				for i := 0; i < 10; i++ {
					Expect(getIPExpectFound(namespace1, service1, clusterID1, "")).To(Equal(serviceIP1))
					Expect(getIPExpectFound(namespace1, service1, clusterID1, clusterID1)).To(Equal(serviceIP1))
					Expect(getIPExpectFound(namespace1, service1, clusterID1, clusterID2)).To(Equal(serviceIP1))
					Expect(getIPExpectFound(namespace1, service1, clusterID1, clusterID3)).To(Equal(serviceIP1))
				}
			})
		})
	})

	When("a service is present in two connected clusters", func() {
		BeforeEach(func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2, port1))
		})

		It("should consistently return the IPs round-robin", func() {
			testRoundRobin(namespace1, service1, "", "", []string{serviceIP1, serviceIP2})
		})

		It("should return the merged service ports", func() {
			Expect(getDNSRecord(namespace1, service1, "", "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
		})

		When("an existent local cluster is specified", func() {
			It("should consistently return its IP", func() {
				for i := 0; i < 10; i++ {
					Expect(getIPExpectFound(namespace1, service1, "", clusterID1)).To(Equal(serviceIP1))
					Expect(getIPExpectFound(namespace1, service1, "", clusterID2)).To(Equal(serviceIP2))
				}
			})
		})

		When("a non-existent local cluster is specified", func() {
			It("should consistently return the IPs round-robin", func() {
				ips := []string{serviceIP1, serviceIP2}
				testRoundRobin(namespace1, service1, "", clusterID3, ips)
				testRoundRobin(namespace1, service1, "", "", ips)
			})
		})
	})

	When("a service is present in three connected clusters", func() {
		JustBeforeEach(func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2, port1, port2))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP3, clusterID3, port1))
		})

		When("no specific cluster is requested", func() {
			It("should consistently return the IPs round-robin", func() {
				ips := []string{serviceIP1, serviceIP2, serviceIP3}
				testRoundRobin(namespace1, service1, "", "", ips)
			})

			It("should consistently return the merged service ports", func() {
				for i := 0; i < 10; i++ {
					Expect(getDNSRecord(namespace1, service1, "", "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
				}

				// Sanity check to ensure per-cluster ports are still preserved.
				Expect(getDNSRecord(namespace1, service1, clusterID2, "").Ports).To(Equal([]mcsv1a1.ServicePort{port1, port2}))
			})
		})

		When("specific cluster is requested", func() {
			It("should consistently return that cluster's IPs", func() {
				firstIP := getClusterIP(namespace1, service1, clusterID2)
				Expect(firstIP).To(Equal(serviceIP2))
				Expect(firstIP).ToNot(Or(Equal(serviceIP1), Equal(serviceIP3)))

				secondIP := getClusterIP(namespace1, service1, clusterID2)
				Expect(secondIP).To(Equal(firstIP))

				thirdIP := getClusterIP(namespace1, service1, clusterID2)
				Expect(thirdIP).To(Equal(firstIP))
			})

			It("should return that cluster's ports", func() {
				Expect(getDNSRecord(namespace1, service1, clusterID1, "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
				Expect(getDNSRecord(namespace1, service1, clusterID2, "").Ports).To(Equal([]mcsv1a1.ServicePort{port1, port2}))
				Expect(getDNSRecord(namespace1, service1, clusterID3, "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))
			})
		})
	})

	When("a service is present in one disconnected cluster", func() {
		It("should consistently return found with empty IP", func() {
			clusterStatusMap[clusterID1] = false
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))

			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(""))
			}
		})
	})

	When("a service is present in two clusters with one disconnected", func() {
		It("should consistently return the IP of the connected cluster", func() {
			clusterStatusMap[clusterID1] = false
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))

			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(serviceIP2))
			}
		})
	})

	When("a service is present in two disconnected clusters", func() {
		It("should consistently return found with empty IP", func() {
			clusterStatusMap[clusterID1] = false
			clusterStatusMap[clusterID2] = false
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))

			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(""))
			}
		})
	})

	When("a service exists in two namespaces", func() {
		It("should return the correct IP for each namespace", func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace2, service1, serviceIP2, clusterID1))

			Expect(getIP(namespace1, service1)).To(Equal(serviceIP1))
			Expect(getIP(namespace2, service1)).To(Equal(serviceIP2))
		})
	})

	When("a service does not exist", func() {
		It("should return not found", func() {
			expectIPsNotFound(namespace1, service1, "", "")
		})
	})

	When("a service IP is updated", func() {
		It("should return the new IP", func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			Expect(getIP(namespace1, service1)).To(Equal(serviceIP1))

			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID1))
			Expect(getIP(namespace1, service1)).To(Equal(serviceIP2))
		})
	})

	When("a service present in one cluster is subsequently removed", func() {
		It("should return not found", func() {
			si := newServiceImport(namespace1, service1, serviceIP1, clusterID1)
			serviceImportMap.Put(si)
			Expect(getIP(namespace1, service1)).To(Equal(serviceIP1))

			serviceImportMap.Remove(si)

			expectIPsNotFound(namespace1, service1, "", "")

			// Should be a no-op
			serviceImportMap.Remove(si)
		})
	})

	When("a service is present in two clusters and one is subsequently removed", func() {
		It("should consistently return the IP and ports of the remaining cluster", func() {
			si1 := newServiceImport(namespace1, service1, serviceIP1, clusterID1, port1)
			serviceImportMap.Put(si1)
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2, port1, port2))

			Expect(getIP(namespace1, service1)).To(Or(Equal(serviceIP1), Equal(serviceIP2)))
			Expect(getDNSRecord(namespace1, service1, "", "").Ports).To(Equal([]mcsv1a1.ServicePort{port1}))

			serviceImportMap.Remove(si1)
			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(serviceIP2))
			}

			Expect(getDNSRecord(namespace1, service1, "", "").Ports).To(Equal([]mcsv1a1.ServicePort{port1, port2}))
		})
	})
})
