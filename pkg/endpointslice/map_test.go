package endpointslice_test

import (
	"sort"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
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

	getIPs := func(hostname, cluster, ns, name string) []string {
		ips, found := endpointSliceMap.GetIPs(hostname, cluster, ns, name, checkCluster)
		Expect(found).To(BeTrue())
		return ips
	}

	expectIPs := func(hostname, cluster, ns, name string, expIPs []string) {
		sort.Strings(expIPs)
		for i := 0; i < 5; i++ {
			ips := getIPs(hostname, cluster, namespace1, service1)
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

				expectIPs("", "", namespace1, service1, []string{endpointIP, endpointIP2})
			})
		})
		When("requested for specific cluster", func() {
			It("should return IPs only from queried cluster", func() {
				es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
				endpointSliceMap.Put(es1)
				es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
				endpointSliceMap.Put(es2)

				expectIPs("", clusterID2, namespace1, service1, []string{endpointIP2})
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

				expectIPs(hostname, clusterID1, namespace1, service1, []string{endpointIP})
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

			expectIPs("", "", namespace1, service1, []string{endpointIP, endpointIP3})
		})
	})

	When("a headless service is present in multiple connected clusters and one is removed", func() {
		It("should consistently return all the remaining IPs", func() {
			es1 := newEndpointSlice(namespace1, service1, clusterID1, []string{endpointIP})
			endpointSliceMap.Put(es1)
			es2 := newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2})
			endpointSliceMap.Put(es2)

			expectIPs("", "", namespace1, service1, []string{endpointIP, endpointIP2})

			endpointSliceMap.Remove(es2)

			expectIPs("", "", namespace1, service1, []string{endpointIP})
		})
	})

})

func newEndpointSlice(namespace, name, clusterID string, endpointIPs []string) *discovery.EndpointSlice {
	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				lhconstants.LabelServiceImportName: name,
				discovery.LabelManagedBy:           lhconstants.LabelValueManagedBy,
				lhconstants.LabelSourceNamespace:   namespace,
				lhconstants.LabelSourceCluster:     clusterID,
				lhconstants.LabelSourceName:        name,
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
