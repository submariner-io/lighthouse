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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	ipv6IP       = "fc00:2001::6757"
	ipv6Hostname = "fc00-2001--6757"
)

var _ = Describe("Dual-stack", func() {
	Describe("ClusterIP Service", testDualStackClusterIPService)
	Describe("Headless Service", testDualStackHeadlessService)
})

func testDualStackClusterIPService() {
	t := newTestDriver()

	BeforeEach(func() {
		t.createServiceImport(newAggregatedServiceImport(namespace1, service1))
	})

	Specify("GetDNSRecords should return the correct DNS record for the requested IP family", func() {
		ipv4EPS := newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1)
		t.createEndpointSlice(ipv4EPS)

		ipv6EPS := newClusterIPEndpointSlice(namespace1, service1, clusterID1, ipv6IP, true, port1)
		ipv6EPS.AddressType = discovery.AddressTypeIPv6
		t.createEndpointSlice(ipv6EPS)

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv4, false, resolver.DNSRecord{
			IP:          serviceIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		})

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv6, false, resolver.DNSRecord{
			IP:          ipv6IP,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		})

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPFamilyUnknown, false, resolver.DNSRecord{
			IP:          serviceIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		})

		By("Deleting the IPv4 EndpointSlice")

		err := t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), ipv4EPS.Name, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		t.awaitDNSRecordsFound(namespace1, service1, "", "", k8snet.IPv4, false)

		t.ensureDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv6, false, resolver.DNSRecord{
			IP:          ipv6IP,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
		})

		By("Deleting the IPv6 EndpointSlice")

		err = t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), ipv6EPS.Name, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		t.awaitDNSRecordsFound(namespace1, service1, "", "", k8snet.IPv6, false)

		By("Deleting the ServiceImport")

		err = t.serviceImports.Namespace(namespace1).Delete(context.TODO(), service1, metav1.DeleteOptions{})
		Expect(err).To(Succeed())

		t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
	})
}

func testDualStackHeadlessService() {
	t := newTestDriver()

	BeforeEach(func() {
		t.createServiceImport(newHeadlessAggregatedServiceImport(namespace1, service1))
	})

	Specify("GetDNSRecords should return the DNS records for both IP families", func() {
		ipv4EPS1 := newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port1},
			discovery.Endpoint{
				Addresses:  []string{endpointIP1},
				Conditions: discovery.EndpointConditions{Ready: &ready},
			},
		)
		t.createEndpointSlice(ipv4EPS1)

		ipv4EPS2 := newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port2},
			discovery.Endpoint{
				Addresses:  []string{endpointIP3},
				Conditions: discovery.EndpointConditions{Ready: &ready},
			},
		)
		t.createEndpointSlice(ipv4EPS2)

		ipv6EPS := newEndpointSlice(namespace1, service1, clusterID1, []mcsv1a1.ServicePort{port3},
			discovery.Endpoint{
				Addresses:  []string{ipv6IP},
				Conditions: discovery.EndpointConditions{Ready: &ready},
			},
		)
		ipv6EPS.AddressType = discovery.AddressTypeIPv6
		t.createEndpointSlice(ipv6EPS)

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv4, true, resolver.DNSRecord{
			IP:          endpointIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
			HostName:    endpointHostname1,
		}, resolver.DNSRecord{
			IP:          endpointIP3,
			Ports:       []mcsv1a1.ServicePort{port2},
			ClusterName: clusterID1,
			HostName:    endpointHostname3,
		})

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPv6, true, resolver.DNSRecord{
			IP:          ipv6IP,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID1,
			HostName:    ipv6Hostname,
		})

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPFamilyUnknown, true, resolver.DNSRecord{
			IP:          endpointIP1,
			Ports:       []mcsv1a1.ServicePort{port1},
			ClusterName: clusterID1,
			HostName:    endpointHostname1,
		}, resolver.DNSRecord{
			IP:          endpointIP3,
			Ports:       []mcsv1a1.ServicePort{port2},
			ClusterName: clusterID1,
			HostName:    endpointHostname3,
		}, resolver.DNSRecord{
			IP:          ipv6IP,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID1,
			HostName:    ipv6Hostname,
		})

		By("Deleting an IPv4 EndpointSlice")

		err := t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), ipv4EPS1.Name, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", k8snet.IPFamilyUnknown, true, resolver.DNSRecord{
			IP:          endpointIP3,
			Ports:       []mcsv1a1.ServicePort{port2},
			ClusterName: clusterID1,
			HostName:    endpointHostname3,
		}, resolver.DNSRecord{
			IP:          ipv6IP,
			Ports:       []mcsv1a1.ServicePort{port3},
			ClusterName: clusterID1,
			HostName:    ipv6Hostname,
		})
	})
}
