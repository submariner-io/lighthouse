package serviceimport_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
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

	getIPs := func(ns, name, cluster string) string {
		ip, found := serviceImportMap.GetIP(ns, name, cluster, checkCluster)
		Expect(found).To(BeTrue())
		return ip
	}

	getIP := func(ns, name string) string {
		ip := getIPs(ns, name, "")
		if ip == "" {
			return ""
		}
		return ip
	}

	getClusterIP := func(ns, name, cluster string) string {
		ip := getIPs(ns, name, cluster)
		if ip == "" {
			return ""
		}
		return ip
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
		JustBeforeEach(func() {
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP1, clusterID1))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP2, clusterID2))
			serviceImportMap.Put(newServiceImport(namespace1, service1, serviceIP3, clusterID3))
		})
		When("no specific cluster is requested", func() {
			It("should consistently return the IPs round-robin", func() {

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
			_, found := serviceImportMap.GetIP(namespace1, service1, "", checkCluster)
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
			_, found := serviceImportMap.GetIP(namespace1, service1, "", checkCluster)
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
})
