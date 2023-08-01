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
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("ServiceImport migration", func() {
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
		t.resolver.PutServiceImport(legacyServiceImport)

		legacyEndpointSlice = newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1)
		legacyEndpointSlice.Endpoints = []discovery.Endpoint{{
			Addresses:  []string{"1.2.3.4"},
			Conditions: discovery.EndpointConditions{Ready: ptr.To(true)},
		}}

		delete(legacyEndpointSlice.Labels, constants.LabelIsHeadless)
		t.resolver.PutEndpointSlices(legacyEndpointSlice)
	})

	When("a legacy per-cluster ServiceImport and EndpointSlice are created", func() {
		Specify("GetDNSRecords should return the cluster's DNS record when requested", func() {
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
		})

		Context("and subsequently deleted", func() {
			Specify("GetDNSRecords should return not found after the EndpointSlice is deleted", func() {
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
			t.resolver.PutServiceImport(newAggregatedServiceImport(namespace1, service1))

			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID2, serviceIP2, true, port1))
		})

		Specify("GetDNSRecords should return the correct DNS records before and after the legacy cluster is upgraded", func() {
			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
			t.assertDNSRecordsFound(namespace1, service1, clusterID2, "", false, cluster2DNSRecord)

			t.resolver.RemoveServiceImport(legacyServiceImport)
			t.putEndpointSlice(newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true, port1))

			t.testRoundRobin(namespace1, service1, serviceIP1, serviceIP2)
			t.assertDNSRecordsFound(namespace1, service1, clusterID1, "", false, cluster1DNSRecord)
			t.assertDNSRecordsFound(namespace1, service1, clusterID2, "", false, cluster2DNSRecord)
		})
	})
})

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
