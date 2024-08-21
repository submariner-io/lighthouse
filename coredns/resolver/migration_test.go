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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Migration", func() {
	Describe("for a ClusterIP Service", testClusterIPServiceMigration)
	Describe("for a Headless Service", testHeadlessServiceMigration)
})

func testClusterIPServiceMigration() {
	t := newTestDriver()

	var (
		legacyServiceImport *mcsv1a1.ServiceImport
		legacyEndpointSlice *discovery.EndpointSlice
	)

	cluster1DNSRecord := resolver.DNSRecord{
		IP:          serviceIP1,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID1,
	}

	cluster2DNSRecord := resolver.DNSRecord{
		IP:          serviceIP2,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID2,
	}

	BeforeEach(func() {
		legacyServiceImport = newLegacyServiceImport(namespace1, service1, serviceIP1, clusterID1, port1)
		t.createServiceImport(legacyServiceImport)

		legacyEndpointSlice = newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1)
		legacyEndpointSlice.Name = fmt.Sprintf("%s-%s-%s", service1, namespace1, clusterID1)
		legacyEndpointSlice.Endpoints = []discovery.Endpoint{{
			Addresses:  []string{"1.2.3.4"},
			Conditions: discovery.EndpointConditions{Ready: ptr.To(true)},
		}}

		delete(legacyEndpointSlice.Labels, constants.LabelIsHeadless)
		t.createEndpointSlice(legacyEndpointSlice)
	})

	Context("with a legacy per-cluster ServiceImport and EndpointSlice", func() {
		Specify("should add its DNS records", func() {
			t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
		})

		Context("that are subsequently deleted", func() {
			Specify("should remove the DNS records", func() {
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)

				t.resolver.RemoveServiceImport(legacyServiceImport)
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)

				t.resolver.RemoveEndpointSlice(legacyEndpointSlice)
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
			})
		})
	})

	Context("with a mix of upgraded and legacy cluster resources", func() {
		BeforeEach(func() {
			t.createServiceImport(newAggregatedServiceImport(namespace1, service1))

			t.createEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, true, port1))
		})

		Specify("the DNS records should be correct before and after the legacy cluster is upgraded", func() {
			t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
			t.awaitDNSRecordsFound(namespace1, service1, clusterID2, "", false, cluster2DNSRecord)
			Eventually(func() string {
				return t.getNonHeadlessDNSRecord(namespace1, service1, "").IP
			}).Should(Equal(serviceIP1))

			t.resolver.RemoveServiceImport(legacyServiceImport)
			t.createEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1))

			t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
			t.awaitDNSRecordsFound(namespace1, service1, clusterID2, "", false, cluster2DNSRecord)
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
		})
	})
}

func testHeadlessServiceMigration() {
	t := newTestDriver()

	endpointIP1DNSRecord := resolver.DNSRecord{
		IP:          endpointIP1,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID1,
	}

	endpointIP2DNSRecord := resolver.DNSRecord{
		IP:          endpointIP2,
		Ports:       []mcsv1a1.ServicePort{port2},
		ClusterName: clusterID1,
	}

	endpointIP3DNSRecord := resolver.DNSRecord{
		IP:          endpointIP3,
		Ports:       []mcsv1a1.ServicePort{port3},
		ClusterName: clusterID1,
	}

	var legacyEndpointSlice *discovery.EndpointSlice

	BeforeEach(func() {
		legacyEndpointSlice = newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port1},
			discovery.Endpoint{
				Addresses:  []string{endpointIP1},
				Conditions: discovery.EndpointConditions{Ready: &ready},
			},
		)
		legacyEndpointSlice.Name = fmt.Sprintf("%s-%s-%s", service1, namespace1, clusterID1)
		legacyEndpointSlice.Annotations = nil

		t.createServiceImport(newHeadlessAggregatedServiceImport(namespace1, service1))

		// Create K8s EndpointSlice.
		t.createEndpointSlice(&discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      service1 + "-adcde",
				Namespace: legacyEndpointSlice.Namespace,
				Labels: map[string]string{
					discovery.LabelServiceName: service1,
				},
			},
			Ports: []discovery.EndpointPort{{
				Name:     ptr.To(port2.Name),
				Protocol: ptr.To(port2.Protocol),
				Port:     ptr.To(port2.Port),
			}},
			Endpoints: []discovery.Endpoint{
				{
					Addresses:  []string{endpointIP2},
					Conditions: discovery.EndpointConditions{Ready: &ready},
				},
			},
		})
	})

	Context("with a pre-0.15 version EndpointSlice", func() {
		JustBeforeEach(func() {
			delete(legacyEndpointSlice.Labels, constants.LabelIsHeadless)
		})

		Context("and no post-0.15 EndpointSlices", func() {
			JustBeforeEach(func() {
				t.createEndpointSlice(legacyEndpointSlice)
			})

			Specify("should add its DNS records", func() {
				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP1DNSRecord)
			})

			Context("that is subsequently deleted", func() {
				Specify("should remove its DNS records", func() {
					t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)
					Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
						metav1.DeleteOptions{})).To(Succeed())
					t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
				})
			})

			Context("on the local cluster", func() {
				BeforeEach(func() {
					t.clusterStatus.SetLocalClusterID(clusterID1)
				})

				Specify("should add the local service DNS records", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP2DNSRecord)
				})
			})
		})

		Context("and an existing post-0.15 EndpointSlice", func() {
			Specify("should ignore the pre-0.15 EndpointSlice", func() {
				By("Creating post-0.15 EndpointSlice")

				t.createEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port3},
					discovery.Endpoint{
						Addresses:  []string{endpointIP3},
						Conditions: discovery.EndpointConditions{Ready: &ready},
					}))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Creating pre-0.15 EndpointSlice")

				t.createEndpointSlice(legacyEndpointSlice)
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Deleting pre-0.15 EndpointSlice")

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
					metav1.DeleteOptions{})).To(Succeed())
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)
			})
		})

		Context("and a post-0.15 EndpointSlice created after", func() {
			Specify("should eventually add the post-0.15 DNS records", func() {
				By("Creating pre-0.15 EndpointSlice")

				t.createEndpointSlice(legacyEndpointSlice)
				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP1DNSRecord)

				By("Creating post-0.15 EndpointSlice")

				t.createEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port3},
					discovery.Endpoint{
						Addresses:  []string{endpointIP3},
						Conditions: discovery.EndpointConditions{Ready: &ready},
					}))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Deleting pre-0.15 EndpointSlice")

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
					metav1.DeleteOptions{})).To(Succeed())
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)
			})
		})
	})

	Context("with a 0.15 version EndpointSlice", func() {
		Context("and no post-0.15 EndpointSlices", func() {
			JustBeforeEach(func() {
				t.createEndpointSlice(legacyEndpointSlice)
			})

			Specify("should add its DNS records", func() {
				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP1DNSRecord)
			})

			Context("that is subsequently deleted", func() {
				Specify("should remove its DNS records", func() {
					t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)
					Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
						metav1.DeleteOptions{})).To(Succeed())
					t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
				})
			})

			Context("on the local cluster", func() {
				BeforeEach(func() {
					t.clusterStatus.SetLocalClusterID(clusterID1)
				})

				Specify("should add the local service endpoints", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP2DNSRecord)
				})
			})
		})

		Context("and an existing post-0.15 EndpointSlice", func() {
			Specify("should ignore the 0.15 EndpointSlice", func() {
				By("Creating post-0.15 EndpointSlice")

				t.createEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port3},
					discovery.Endpoint{
						Addresses:  []string{endpointIP3},
						Conditions: discovery.EndpointConditions{Ready: &ready},
					}))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Creating 0.15 EndpointSlice")

				t.createEndpointSlice(legacyEndpointSlice)
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Deleting 0.15 EndpointSlice")

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
					metav1.DeleteOptions{})).To(Succeed())
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)
			})
		})

		Context("and a post-0.15 EndpointSlice is created after", func() {
			Specify("should eventually add the post-0.15 DNS records", func() {
				By("Creating 0.15 EndpointSlice")

				t.createEndpointSlice(legacyEndpointSlice)
				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP1DNSRecord)

				By("Creating post-0.15 EndpointSlice")

				t.createEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port3},
					discovery.Endpoint{
						Addresses:  []string{endpointIP3},
						Conditions: discovery.EndpointConditions{Ready: &ready},
					}))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)

				By("Deleting 0.15 EndpointSlice")

				Expect(t.endpointSlices.Namespace(namespace1).Delete(context.Background(), legacyEndpointSlice.Name,
					metav1.DeleteOptions{})).To(Succeed())
				t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", true, endpointIP3DNSRecord)
			})
		})
	})
}

func newLegacyServiceImport(namespace, name, serviceIP, clusterID string, ports ...mcsv1a1.ServicePort) *mcsv1a1.ServiceImport {
	var ips []string
	if serviceIP != "" {
		ips = []string{serviceIP}
	}

	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: submarinerNamespace,
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
			Labels: map[string]string{
				"lighthouse.submariner.io/sourceName":    name,
				constants.LabelSourceNamespace:           namespace,
				"lighthouse.submariner.io/sourceCluster": clusterID,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  mcsv1a1.ClusterSetIP,
			IPs:   ips,
			Ports: ports,
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID,
				},
			},
		},
	}
}
