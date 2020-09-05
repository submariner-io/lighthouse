package serviceimport_test

import (
	"sort"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
)

var _ = Describe("ServiceImport Map", func() {
	const (
		service1   = "service1"
		namespace1 = "namespace1"
		namespace2 = "namespace2"
		serviceIP1 = "192.168.56.21"
		serviceIP2 = "192.168.56.22"
		serviceIP3 = "192.168.56.23"
		clusterID1 = "clusterID1"
		clusterID2 = "clusterID2"
		clusterID3 = "clusterID3"
	)

	var (
		clusterStatusMap map[string]bool
		serviceImportMap *serviceimport.Map
	)

	BeforeEach(func() {
		clusterStatusMap = map[string]bool{clusterID1: true, clusterID2: true, clusterID3: true}
		serviceImportMap = serviceimport.NewMap()
	})

	checkCluster := func(id string) bool {
		return clusterStatusMap[id]
	}

	getIPs := func(ns, name string) []string {
		ips, found := serviceImportMap.GetIPs(ns, name, "", checkCluster)
		Expect(found).To(BeTrue())
		return ips
	}

	getIP := func(ns, name string) string {
		ips := getIPs(ns, name)
		if len(ips) == 0 {
			return ""
		}
		Expect(ips).To(HaveLen(1))
		return ips[0]
	}

	expectIPs := func(ns, name string, expIPs []string) {
		sort.Strings(expIPs)
		for i := 0; i < 5; i++ {
			ips := getIPs(namespace1, service1)
			sort.Strings(ips)
			Expect(ips).To(Equal(expIPs))
		}
	}

	When("a service is present in one connected cluster", func() {
		It("should consistently return the same IP", func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))

			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(serviceIP1))
			}
		})
	})

	When("a service is present in two connected clusters", func() {
		It("should consistently return the IPs round-robin", func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))

			firstIP := getIP(namespace1, service1)
			Expect(firstIP).To(Or(Equal(serviceIP1), Equal(serviceIP2)))

			secondIP := getIP(namespace1, service1)
			Expect(secondIP).To(Or(Equal(serviceIP1), Equal(serviceIP2)))
			Expect(secondIP).ToNot(Equal(firstIP))

			for i := 0; i < 5; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(firstIP))
				Expect(getIP(namespace1, service1)).To(Equal(secondIP))
			}
		})
	})

	When("a service is present in three connected clusters", func() {
		It("should consistently return the IPs round-robin", func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP3, clusterID3))

			firstIP := getIP(namespace1, service1)
			Expect(firstIP).To(Or(Equal(serviceIP1), Equal(serviceIP2), Equal(serviceIP3)))

			secondIP := getIP(namespace1, service1)
			Expect(secondIP).To(Or(Equal(serviceIP1), Equal(serviceIP2), Equal(serviceIP3)))
			Expect(secondIP).ToNot(Equal(firstIP))

			thirdIP := getIP(namespace1, service1)
			Expect(thirdIP).To(Or(Equal(serviceIP1), Equal(serviceIP2), Equal(serviceIP3)))
			Expect(thirdIP).ToNot(Equal(firstIP))
			Expect(thirdIP).ToNot(Equal(secondIP))

			for i := 0; i < 5; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(firstIP))
				Expect(getIP(namespace1, service1)).To(Equal(secondIP))
				Expect(getIP(namespace1, service1)).To(Equal(thirdIP))
			}
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
			_, found := serviceImportMap.GetIPs(namespace1, service1, "", checkCluster)
			Expect(found).To(BeFalse())
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
			_, found := serviceImportMap.GetIPs(namespace1, service1, "", checkCluster)
			Expect(found).To(BeFalse())

			// Should be a no-op
			serviceImportMap.Remove(si)
		})
	})

	When("a service is present in two clusters and one is subsequently removed", func() {
		It("should consistently return the IP of the remaining cluster", func() {
			si1 := newServiceImport(namespace1, service1, serviceIP1, clusterID1)
			serviceImportMap.Put(si1)
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))
			Expect(getIP(namespace1, service1)).To(Or(Equal(serviceIP1), Equal(serviceIP2)))

			serviceImportMap.Remove(si1)
			for i := 0; i < 10; i++ {
				Expect(getIP(namespace1, service1)).To(Equal(serviceIP2))
			}
		})
	})

	When("a headless service is present in multiple connected clusters", func() {
		It("should consistently return all the IPs", func() {
			serviceImport1 := newHeadlessServiceImport(namespace1, service1, clusterID1, "10.253.1.1", "10.253.1.2")
			serviceImportMap.Put(serviceImport1)
			serviceImport2 := newHeadlessServiceImport(namespace1, service1, clusterID2, "10.253.2.1")
			serviceImportMap.Put(serviceImport2)
			serviceImportMap.Put(newHeadlessServiceImport(namespace1, service1, clusterID3))

			expectIPs(namespace1, service1, append(serviceImport1.Status.Clusters[0].IPs, serviceImport2.Status.Clusters[0].IPs...))
		})
	})

	When("a headless service is present in multiple connected clusters with one disconnected", func() {
		It("should consistently return all the IPs from the connected clusters", func() {
			serviceImport1 := newHeadlessServiceImport(namespace1, service1, clusterID1, "10.253.1.1", "10.253.1.2")
			serviceImportMap.Put(serviceImport1)
			serviceImport2 := newHeadlessServiceImport(namespace1, service1, clusterID2, "10.253.2.1", "10.253.2.2")
			serviceImportMap.Put(serviceImport2)
			serviceImport3 := newHeadlessServiceImport(namespace1, service1, clusterID3, "10.253.3.1", "10.253.3.2")
			serviceImportMap.Put(serviceImport3)

			clusterStatusMap[clusterID2] = false
			expectIPs(namespace1, service1, append(serviceImport1.Status.Clusters[0].IPs, serviceImport3.Status.Clusters[0].IPs...))
		})
	})

	When("a headless service is present in multiple connected clusters and one is removed", func() {
		It("should consistently return all the remaining IPs", func() {
			serviceImport1 := newHeadlessServiceImport(namespace1, service1, clusterID1, "10.253.1.1")
			serviceImportMap.Put(serviceImport1)
			serviceImport2 := newHeadlessServiceImport(namespace1, service1, clusterID2, "10.253.2.1", "10.253.2.2", "10.253.2.3")
			serviceImportMap.Put(serviceImport2)

			expectIPs(namespace1, service1, append(serviceImport1.Status.Clusters[0].IPs, serviceImport2.Status.Clusters[0].IPs...))

			serviceImportMap.Remove(serviceImport2)
			expectIPs(namespace1, service1, serviceImport1.Status.Clusters[0].IPs)
		})
	})
})

func newHeadlessServiceImport(namespace, name, clusterID string, ips ...string) *lighthousev2a1.ServiceImport {
	si := newServiceImport(namespace, name, "", clusterID)
	si.Spec.Type = lighthousev2a1.Headless
	si.Status.Clusters[0].IPs = ips

	return si
}
