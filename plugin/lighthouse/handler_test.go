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
package lighthouse

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
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
	portName1   = "http"
	portName2   = "dns"
	protocol1   = v1.ProtocolTCP
	portNumber1 = int32(8080)
	protocol2   = v1.ProtocolUDP
	portNumber2 = int32(53)
	hostName1   = "hostName1"
	hostName2   = "hostName2"
)

var _ = Describe("Lighthouse DNS plugin Handler", func() {
	Context("Fallthrough not configured", testWithoutFallback)
	Context("Fallthrough configured", testWithFallback)
	Context("Cluster connectivity status", testClusterStatus)
	Context("Headless services", testHeadlessService)
	Context("Local services", testLocalService)
	Context("SRV  records", testSRVMultiplePorts)
})

type FailingResponseWriter struct {
	test.ResponseWriter
	errorMsg string
}

type MockClusterStatus struct {
	clusterStatusMap map[string]bool
	localClusterID   string
}

func NewMockClusterStatus() *MockClusterStatus {
	return &MockClusterStatus{clusterStatusMap: make(map[string]bool), localClusterID: ""}
}

func (m *MockClusterStatus) IsConnected(clusterID string) bool {
	return m.clusterStatusMap[clusterID]
}

type MockEndpointStatus struct {
	endpointStatusMap map[string]bool
}

func NewMockEndpointStatus() *MockEndpointStatus {
	return &MockEndpointStatus{endpointStatusMap: make(map[string]bool)}
}

func (m *MockEndpointStatus) IsHealthy(name, namespace, clusterID string) bool {
	return m.endpointStatusMap[clusterID]
}

func (m *MockClusterStatus) LocalClusterID() string {
	return m.localClusterID
}

type MockLocalServices struct {
	LocalServicesMap map[string]*serviceimport.DNSRecord
}

func NewMockLocalServices() *MockLocalServices {
	return &MockLocalServices{LocalServicesMap: make(map[string]*serviceimport.DNSRecord)}
}

func (m *MockLocalServices) GetIP(name, namespace string) (*serviceimport.DNSRecord, bool) {
	record, found := m.LocalServicesMap[getKey(name, namespace)]
	return record, found
}

func getKey(name, namespace string) string {
	return namespace + "/" + name
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
		mockEs := NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockLs := NewMockLocalServices()
		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			serviceImports:  setupServiceImportMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("DNS query for an existing service", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("of Type A record should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("of Type SRV should succeed and write an SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
	})

	When("DNS query for an existing service in specific cluster", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)
		It("of Type A record should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Qtype: dns.TypeA,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		It("of Type SRV should succeed and write an SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qtype: dns.TypeSRV,
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
	})

	When("DNS query for an existing service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)
		It("of Type A record should succeed and write an A record response", func() {
			lh.serviceImports.Put(newServiceImport(namespace2, service1, clusterID, serviceIP, portName1,
				portNumber1, protocol1, mcsv1a1.ClusterSetIP))
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("of Type SRV should succeed and write an SRV record response", func() {
			lh.serviceImports.Put(newServiceImport(namespace2, service1, clusterID, serviceIP, portName1, portNumber1,
				protocol1, mcsv1a1.ClusterSetIP))
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
	})

	When("DNS query for a non-existent service", func() {
		qname := fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1)
		It("of Type A record should return RcodeNameError for A record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a non-existent service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)
		It("of Type A record should return RcodeNameError for A record query ", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query ", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)
		It("of Type A record should return RcodeNameError for A record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a non-existent zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace2)
		It("of Type A record should return RcodeNameError for A record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty record", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
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

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return error RcodeServerFailure", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
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
		mockCs.localClusterID = clusterID
		mockEs := NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockLs := NewMockLocalServices()

		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			Fall:            fall.F{Zones: []string{"clusterset.local."}},
			Next:            test.NextHandler(dns.RcodeBadCookie, errors.New("dummy plugin")),
			serviceImports:  setupServiceImportMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("type A DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)
		It("should invoke the next plugin", func() {
			lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)
		It("should not invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty record", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeAAAA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("type A DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-existent service", func() {
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		It("should invoke the next plugin", func() {
			lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			executeTestCase(lh, rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		It("should not invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type SRV DNS query for a pod", func() {
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-existent service", func() {
		It("should invoke the next plugin", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeSRV,
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
		mockEs := NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockEs.endpointStatusMap[clusterID2] = true
		mockLs := NewMockLocalServices()
		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			serviceImports:  setupServiceImportMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}
		lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2,
			portNumber2, protocol2, mcsv1a1.ClusterSetIP))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("service is in two clusters and specific cluster is requested", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID2, service1, namespace1)
		It("should succeed and write that cluster's IP as A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qtype: dns.TypeA,
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})

		It("should succeed and write that cluster's IP as SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
				},
			})
		})
	})

	When("service is in two connected clusters and one is not of type ClusterSetIP", func() {
		JustBeforeEach(func() {
			lh.serviceImports = setupServiceImportMap()
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2,
				portNumber2, protocol2, ""))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response with the available IP", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("should succeed and write that cluster's IP as SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
	})

	When("service is in two clusters and only one is connected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})
		It("should succeed and write an SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
				},
			})
		})
	})

	When("service is present in two clusters and both are disconnected", func() {
		JustBeforeEach(func() {
			mockCs.clusterStatusMap[clusterID] = false
			mockCs.clusterStatusMap[clusterID2] = false
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty response (NODATA) for A record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should return empty response (NODATA) for SRV record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
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
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty response (NODATA) for A record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should return empty response (NODATA) for SRV record query", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
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
		mockEs *MockEndpointStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		mockCs.localClusterID = clusterID
		mockEs = NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockEs.endpointStatusMap[clusterID2] = true
		mockLs := NewMockLocalServices()
		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			serviceImports:  serviceimport.NewMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("headless service has no IPs", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{}, []string{}, portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and return empty response (NODATA)", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should succeed and return empty response (NODATA)", func() {
			executeTestCase(lh, rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})
	When("headless service has one IP", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP},
				portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP)),
				},
			})
		})
		It("should succeed and write an SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.%s", qname, portNumber1, hostName1, clusterID, qname)),
				},
			})
		})
		It("should succeed and write an SRV record response for query with cluster name", func() {
			qname = fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s", qname, portNumber1, hostName1, qname)),
				},
			})
		})
	})

	When("headless service has two IPs", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1, portNumber1, protocol1,
				mcsv1a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1, hostName2},
				[]string{endpointIP, endpointIP2},
				portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write two A records as response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP)),
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP2)),
				},
			})
		})
		It("should succeed and write an SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s", qname, portNumber1, hostName1, clusterID, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s", qname, portNumber1, hostName2, clusterID, qname)),
				},
			})
		})
		It("should succeed and write an SRV record response when port and protocol is queried", func() {
			qname = fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", portName1, protocol1, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, portNumber1, hostName1, clusterID, service1, namespace1)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, portNumber1, hostName2, clusterID, service1, namespace1)),
				},
			})
		})
		It("should succeed and write an SRV record response when port and protocol is queried with underscore prefix", func() {
			qname = fmt.Sprintf("_%s._%s.%s.%s.svc.clusterset.local.", portName1, protocol1, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, portNumber1, hostName1, clusterID, service1, namespace1)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, portNumber1, hostName2, clusterID, service1, namespace1)),
				},
			})
		})
	})

	When("headless service is present in two clusters", func() {
		JustBeforeEach(func() {
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP},
				portNumber1, protocol1))
			lh.endpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID2, portName1, []string{hostName2}, []string{endpointIP2},
				portNumber1, protocol1))
			mockCs.clusterStatusMap[clusterID2] = true
		})
		When("no cluster is requested", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
			It("should succeed and write all IPs as A records in response", func() {
				executeTestCase(lh, rec, test.Case{
					Qname: qname,
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP)),
						test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP2)),
					},
				})
			})
		})
		When("requested for a specific cluster", func() {
			qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)
			It("should succeed and write the cluster's IP as A record in response", func() {
				executeTestCase(lh, rec, test.Case{
					Qname: qname,
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP)),
					},
				})
			})
		})
	})
}

func testLocalService() {
	var (
		rec    *dnstest.Recorder
		lh     *Lighthouse
		mockCs *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		mockCs.clusterStatusMap[clusterID2] = true
		mockEs := NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockEs.endpointStatusMap[clusterID2] = true
		mockLs := NewMockLocalServices()
		mockCs.localClusterID = clusterID
		mockLs.LocalServicesMap[getKey(service1, namespace1)] = &serviceimport.DNSRecord{
			IP: serviceIP,
			Ports: []mcsv1a1.ServicePort{
				{
					Name:        portName1,
					Protocol:    protocol1,
					AppProtocol: nil,
					Port:        portNumber1,
				},
			},
			ClusterName: clusterID,
		}
		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			serviceImports:  setupServiceImportMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}
		lh.serviceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2, portNumber2,
			protocol2, mcsv1a1.ClusterSetIP))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("service is in local and remote clusters", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write local cluster's IP as A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
			// Execute again to make sure not round robin
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("should succeed and write local cluster's IP as SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
	})

	When("service is in local and remote clusters, and remote cluster is requested", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID2, service1, namespace1)
		It("should succeed and write remote cluster's IP as A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})

		It("should succeed and write remote cluster's IP as SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
				},
			})
		})
	})

	When("service is in local and remote clusters, and local has no active endpoints", func() {
		JustBeforeEach(func() {
			mockEs := NewMockEndpointStatus()
			mockEs.endpointStatusMap[clusterID] = false
			mockEs.endpointStatusMap[clusterID2] = true
			lh.endpointsStatus = mockEs
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write remote cluster's IP as A record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})
		It("should succeed and write remote cluster's IP as SRV record response", func() {
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
				},
			})
		})
	})
}

func testSRVMultiplePorts() {
	var (
		rec    *dnstest.Recorder
		lh     *Lighthouse
		mockCs *MockClusterStatus
	)

	BeforeEach(func() {
		mockCs = NewMockClusterStatus()
		mockCs.clusterStatusMap[clusterID] = true
		mockEs := NewMockEndpointStatus()
		mockEs.endpointStatusMap[clusterID] = true
		mockLs := NewMockLocalServices()
		mockCs.localClusterID = clusterID
		mockLs.LocalServicesMap[getKey(service1, namespace1)] = &serviceimport.DNSRecord{
			IP: serviceIP,
			Ports: []mcsv1a1.ServicePort{
				{
					Name:        portName1,
					Protocol:    protocol1,
					AppProtocol: nil,
					Port:        portNumber1,
				},
				{
					Name:        portName2,
					Protocol:    protocol2,
					AppProtocol: nil,
					Port:        portNumber2,
				},
			},
			ClusterName: clusterID,
		}
		lh = &Lighthouse{
			Zones:           []string{"clusterset.local."},
			serviceImports:  setupServiceImportMap(),
			endpointSlices:  setupEndpointSliceMap(),
			clusterStatus:   mockCs,
			endpointsStatus: mockEs,
			localServices:   mockLs,
			ttl:             defaultTTL,
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("DNS query of type SRV", func() {
		It("without portName should return all the ports", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
		It("with  HTTP portname  should return TCP port", func() {
			qname := fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", portName1, protocol1, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, portNumber1, service1, namespace1)),
				},
			})
		})
		It("with  DNS portname  should return UDP port", func() {
			qname := fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", portName2, protocol2, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, portNumber2, service1, namespace1)),
				},
			})
		})
		It("with  cluster name should return all the ports from the cluster", func() {
			qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber2, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, portNumber1, qname)),
				},
			})
		})
		It("with  HTTP portname  should return TCP port with underscore prefix", func() {
			qname := fmt.Sprintf("_%s._%s.%s.%s.svc.clusterset.local.", portName1, protocol1, service1, namespace1)
			executeTestCase(lh, rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, portNumber1, service1, namespace1)),
				},
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
	siMap.Put(newServiceImport(namespace1, service1, clusterID, serviceIP, portName1, portNumber1, protocol1, mcsv1a1.ClusterSetIP))

	return siMap
}

func setupEndpointSliceMap() *endpointslice.Map {
	esMap := endpointslice.NewMap()
	esMap.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP}, portNumber1, protocol1))

	return esMap
}

func newServiceImport(namespace, name, clusterID, serviceIP, portName string,
	portNumber int32, protocol v1.Protocol, siType mcsv1a1.ServiceImportType) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
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
		Spec: mcsv1a1.ServiceImportSpec{
			Type: siType,
			IPs:  []string{serviceIP},
			Ports: []mcsv1a1.ServicePort{
				{
					Name:     portName,
					Protocol: protocol,
					Port:     portNumber,
				},
			},
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

func newEndpointSlice(namespace, name, clusterID, portName string, hostName, endpointIPs []string, portNumber int32,
	protocol v1.Protocol) *discovery.EndpointSlice {
	endpoints := make([]discovery.Endpoint, len(endpointIPs))

	for i := range endpointIPs {
		endpoint := discovery.Endpoint{
			Addresses: []string{endpointIPs[i]},
			Hostname:  &hostName[i],
		}
		endpoints[i] = endpoint
	}

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
		Endpoints:   endpoints,
		Ports: []discovery.EndpointPort{
			{
				Name:     &portName,
				Protocol: &protocol,
				Port:     &portNumber,
			},
		},
	}
}
