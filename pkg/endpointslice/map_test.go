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
package endpointslice_test

import (
	"sort"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("EndpointSlice Map", func() {
	const (
		service1    = "service1"
		namespace1  = "namespace1"
		clusterID1  = "clusterID1"
		clusterID2  = "clusterID2"
		clusterID3  = "clusterID3"
		endpointIP  = "100.96.157.101"
		endpointIP2 = "100.96.157.102"
		endpointIP3 = "100.96.157.103"
	)

	var (
		clusterStatusMap map[string]bool
		endpointSliceMap *endpointslice.Map
	)

	BeforeEach(func() {
		clusterStatusMap = map[string]bool{clusterID1: true, clusterID2: true, clusterID3: true}
		endpointSliceMap = endpointslice.NewMap()
	})

	checkCluster := func(id string) bool {
		return clusterStatusMap[id]
	}

	getRecords := func(hostname, cluster, ns, name string) []serviceimport.DNSRecord {
		ips, found := endpointSliceMap.GetDNSRecords(hostname, cluster, ns, name, checkCluster)
		Expect(found).To(BeTrue())
		return ips
	}

	expectIPs := func(hostname, cluster string, expIPs []string) {
		sort.Strings(expIPs)
		for i := 0; i < 5; i++ {
			var ips []string
			records := getRecords(hostname, cluster, namespace1, service1)
			for _, record := range records {
				ips = append(ips, record.IP)
			}
			sort.Strings(ips)
			Expect(ips).To(Equal(expIPs))
		}
	}

	When("a headless service is present in multiple connected clusters", func() {
		When("no specific cluster is queried", func() {
			It("should consistently return all the IPs", func() {
				es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
				endpointSliceMap.Put(es1)
				es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
				endpointSliceMap.Put(es2)

				expectIPs("", "", []string{endpointIP, endpointIP2})
			})
		})
		When("requested for specific cluster", func() {
			It("should return IPs only from queried cluster", func() {
				es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
				endpointSliceMap.Put(es1)
				es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
				endpointSliceMap.Put(es2)

				expectIPs("", clusterID2, []string{endpointIP2})
			})
		})
		When("specific host is queried", func() {
			It("should return IPs from specific host", func() {
				hostname := "host1"
				es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
				es1.Endpoints[0].Hostname = &hostname
				endpointSliceMap.Put(es1)
				es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
				endpointSliceMap.Put(es2)

				expectIPs(hostname, clusterID1, []string{endpointIP})
			})
		})
	})

	When("a headless service is present in multiple connected clusters with one disconnected", func() {
		It("should consistently return all the IPs from the connected clusters", func() {
			es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
			endpointSliceMap.Put(es1)
			es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
			endpointSliceMap.Put(es2)
			es3 := newEndpointSlice(namespace1, service1, clusterID3, []string{endpointIP3})
			endpointSliceMap.Put(es3)

			clusterStatusMap[clusterID2] = false

			expectIPs("", "", []string{endpointIP, endpointIP3})
		})
	})

	When("a headless service is present in multiple connected clusters and one is removed", func() {
		It("should consistently return all the remaining IPs", func() {
			es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
			endpointSliceMap.Put(es1)
			es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
			endpointSliceMap.Put(es2)

			expectIPs("", "", []string{endpointIP, endpointIP2})

			endpointSliceMap.Remove(es2)

			expectIPs("", "", []string{endpointIP})
		})
	})
})

// nolint:unparam // `namespace` always receives `namespace1`.
func newEndpointSlice(namespace, name, clusterID string, endpointIPs []string) *discovery.EndpointSlice {
	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				discovery.LabelManagedBy:          lhconstants.LabelValueManagedBy,
				lhconstants.LabelSourceNamespace:  namespace,
				lhconstants.MCSLabelSourceCluster: clusterID,
				lhconstants.MCSLabelServiceName:   name,
			},
		},
		AddressType: discovery.AddressTypeIPv4,
		Endpoints: []discovery.Endpoint{
			{
				Addresses: endpointIPs,
			},
		},
	}
}
