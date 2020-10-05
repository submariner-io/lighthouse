package lighthouse

import (
	"context"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	service1    = "service1"
	namespace1  = "namespace1"
	namespace2  = "namespace2"
	serviceIP   = "100.96.156.101"
	serviceIP2  = "100.96.156.102"
	clusterID   = "cluster1"
	clusterID2  = "cluster2"
	endpointIP  = "100.96.157.101"
	endpointIP2 = "100.96.157.102"
)

var _ = Describe("Lighthouse DNS plugin Handler", func() {
	Context("Fallthrough not configured", testWithoutFallback)
	Context("Fallthrough configured", testWithFallback)
	Context("Cluster connectivity status", testClusterStatus)
	Context("Headless services", testHeadlessService)
})

type FailingResponseWriter struct {
	test.ResponseWriter
	errorMsg string
}

type MockClusterStatus struct {
	clusterStatusMap map[string]bool
}

func NewMockClusterStatus() *MockClusterStatus {
	return &MockClusterStatus{clusterStatusMap: make(map[string]bool)}
}

func (m *MockClusterStatus) IsConnected(clusterId string) bool {
	return m.clusterStatusMap[clusterId]
}

func (w *FailingResponseWriter) WriteMsg(m *dns.Msg) error {
	return errors.New(w.errorMsg)
}

func testWithoutFallback() {
	var (
		rec *dnstest.Recorder
		lh  *Lighthouse
	)

	BeforeEach(func() {
		mockCs := NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		lh = &Lighthouse{
			Zones:          []string{"clusterset.local."},
			serviceImports: setupServiceImportMap(),
			endpointSlices: setupEndpointSliceMap(),
			clusterStatus:  mockCs,
			ttl:            defaultTtl,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("type A DNS query for an existing service", func() {
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + serviceIP),
				},
			})
		})
	})

	When("type A DNS query for an existing service in specific cluster", func() {
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: clusterID + "." + service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(clusterID + "." + service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + serviceIP),
				},
			})
		})
	})

	When("type A DNS query for an existing service with a different namespace", func() {
		It("should succeed and write an A record response", func() {
			lh.serviceImports.Put(newServiceImport(namespace2, service1, clusterID, serviceIP, lighthousev2a1.ClusterSetIP))
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace2 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace2 + ".svc.clusterset.local.    5    IN    A    " + serviceIP),
				},
			})
		})
	})

	When("type A DNS query for a non-existent service", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: "unknown." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("type A DNS query for a non-existent service with a different namespace", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace2 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("type A DNS query for a pod", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".pod.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("type A DNS query for a non-existent zone", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace2 + ".svc.cluster.east.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		It("should return empty record", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype:  dns.TypeAAAA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("writing the response message fails", func() {
		BeforeEach(func() {
			rec = dnstest.NewRecorder(&FailingResponseWriter{errorMsg: "write failed"})
		})

		It("should return error RcodeServerFailure", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeServerFailure,
			})
		})
	})
}

func testWithFallback() {
	var (
		rec *dnstest.Recorder
		lh  *Lighthouse
	)

	BeforeEach(func() {
		mockCs := NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		lh = &Lighthouse{
			Zones:          []string{"clusterset.local."},
			Fall:           fall.F{Zones: []string{"clusterset.local."}},
			Next:           test.NextHandler(dns.RcodeBadCookie, errors.New("dummy plugin")),
			serviceImports: setupServiceImportMap(),
			endpointSlices: setupEndpointSliceMap(),
			clusterStatus:  mockCs,
			ttl:            defaultTtl,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("type A DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		It("should invoke the next plugin", func() {
			lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.cluster.east.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		It("should not invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.cluster.east.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		It("should return empty record", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype:  dns.TypeAAAA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("type A DNS query for a pod", func() {
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".pod.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-existent service", func() {
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: "unknown." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})
}

func testClusterStatus() {
	var (
		rec    *dnstest.Recorder
		lh     *Lighthouse
		mockCs *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		mockCs.clusterStatusMap[clusterID2] = true
		lh = &Lighthouse{
			Zones:          []string{"clusterset.local."},
			serviceImports: setupServiceImportMap(),
			endpointSlices: setupEndpointSliceMap(),
			clusterStatus:  mockCs,
			ttl:            defaultTtl,
		}
		lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, lighthousev2a1.ClusterSetIP))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("service is in two clusters and specific cluster is requested", func() {
		It("should succeed and write that cluster's IP as A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: clusterID2 + "." + service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(clusterID2 + "." + service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + serviceIP2),
				},
			})
		})
	})

	When("service is in two connected clusters and one has no IP", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP, ""))
		})
		It("should succeed and write an A record response with the available IP", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + serviceIP),
				},
			})
		})
	})

	When("service is in two clusters and only one is connected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
		})
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + serviceIP2),
				},
			})
		})
	})

	When("service is present in two clusters and both are disconnected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
			mockCs.clusterStatusMap[clusterID2] = false
		})
		It("should return empty response (NODATA)", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("service is present in one cluster and it is disconnected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
			delete(mockCs.clusterStatusMap, clusterID2)
			lh.serviceImports = setupServiceImportMap()
		})
		It("should return empty response (NODATA)", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})
}

func testHeadlessService() {
	var (
		rec    *dnstest.Recorder
		lh     *Lighthouse
		mockCs *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		lh = &Lighthouse{
			Zones:          []string{"clusterset.local."},
			serviceImports: serviceimport.NewMap(),
			endpointSlices: setupEndpointSliceMap(),
			clusterStatus:  mockCs,
			ttl:            defaultTtl,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("headless service has no IPs", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", lighthousev2a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, []string{}))
		})
		It("should succeed and return empty response (NODATA)", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("headless service has one IP", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", lighthousev2a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, []string{endpointIP}))
		})
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP),
				},
			})
		})
	})

	When("headless service has two IPs", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", lighthousev2a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, []string{endpointIP, endpointIP2}))
		})
		It("should succeed and write two A records as response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP),
					test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP2),
				},
			})
		})
	})

	When("headless service is present in two clusters", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", lighthousev2a1.Headless))
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, "", lighthousev2a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, []string{endpointIP}))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID2, []string{endpointIP2}))
			mockCs.clusterStatusMap[clusterID2] = true
		})
		When("no cluster is requested", func() {
			It("should succeed and write all IPs as A records in response", func() {
				executeTestCase(lh, rec, test.Case{
					Qname: service1 + "." + namespace1 + ".svc.clusterset.local.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP),
						test.A(service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP2),
					},
				})
			})
		})
		When("requested for a specific cluster", func() {
			It("should succeed and write the cluster's IP as A record in response", func() {
				executeTestCase(lh, rec, test.Case{
					Qname: clusterID + "." + service1 + "." + namespace1 + ".svc.clusterset.local.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A(clusterID + "." + service1 + "." + namespace1 + ".svc.clusterset.local.    5    IN    A    " + endpointIP),
					},
				})
			})
		})
	})
}

func executeTestCase(lh *Lighthouse, rec *dnstest.Recorder, tc test.Case) {
	code, err := lh.ServeDNS(context.TODO(), rec, tc.Msg())

	Expect(code).Should(Equal(tc.Rcode))

	if tc.Rcode == dns.RcodeSuccess {
		Expect(err).To(Succeed())
		Expect(test.SortAndCheck(rec.Msg, tc)).To(Succeed())
	} else {
		Expect(err).To(HaveOccurred())
	}
}

func setupServiceImportMap() *serviceimport.Map {
	siMap := serviceimport.NewMap()
	siMap.Put(newServiceImport(namespace1, service1, clusterID, serviceIP, lighthousev2a1.ClusterSetIP))

	return siMap
}

func setupEndpointSliceMap() *endpointslice.Map {
	esMap := endpointslice.NewMap()
	esMap.Put(newEndpointSlice(namespace1, service1, clusterID, []string{endpointIP}))

	return esMap
}

func newServiceImport(namespace, name, clusterID, serviceIP string,
	siType lighthousev2a1.ServiceImportType) *lighthousev2a1.ServiceImport {
	return &lighthousev2a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
			Labels: map[string]string{
				lhconstants.LabelSourceCluster: clusterID,
			},
		},
		Spec: lighthousev2a1.ServiceImportSpec{
			Type: siType,
			IP:   serviceIP,
		},
		Status: lighthousev2a1.ServiceImportStatus{
			Clusters: []lighthousev2a1.ClusterStatus{
				{
					Cluster: clusterID,
				},
			},
		},
	}
}

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
