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
package lighthouse_test

import (
	"context"
	"fmt"

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
	"github.com/submariner-io/lighthouse/plugin/lighthouse"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	service1       = "service1"
	namespace1     = "namespace1"
	namespace2     = "namespace2"
	serviceIP      = "100.96.156.101"
	serviceIP2     = "100.96.156.102"
	localClusterID = "local"
	clusterID      = "cluster1"
	clusterID2     = "cluster2"
	endpointIP     = "100.96.157.101"
	endpointIP2    = "100.96.157.102"
	portName1      = "http"
	portName2      = "dns"
	protocol1      = v1.ProtocolTCP
	portNumber1    = int32(8080)
	protocol2      = v1.ProtocolUDP
	portNumber2    = int32(53)
	hostName1      = "hostName1"
	hostName2      = "hostName2"
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
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockEs.endpointStatusMap[clusterID] = true

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("DNS query for an existing service", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("of Type A record should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Qtype: dns.TypeA,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		It("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.lh.ServiceImports.Put(newServiceImport(namespace2, service1, clusterID, serviceIP, portName1,
				portNumber1, protocol1, mcsv1a1.ClusterSetIP))
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("of Type SRV should succeed and write an SRV record response", func() {
			t.lh.ServiceImports.Put(newServiceImport(namespace2, service1, clusterID, serviceIP, portName1, portNumber1,
				protocol1, mcsv1a1.ClusterSetIP))
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a non-existent service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)
		It("of Type A record should return RcodeNameError for A record query ", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query ", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)
		It("of Type A record should return RcodeNameError for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("DNS query for a non-existent zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace2)
		It("of Type A record should return RcodeNameError for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
		It("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty record", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockCs.localClusterID = clusterID
		t.mockEs.endpointStatusMap[clusterID] = true
		t.lh.Fall = fall.F{Zones: []string{"clusterset.local."}}
		t.lh.Next = test.NextHandler(dns.RcodeBadCookie, errors.New("dummy plugin"))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("type A DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)
		It("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)
		It("should not invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty record", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type A DNS query for a non-existent service", func() {
		It("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		It("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		It("should not invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	When("type SRV DNS query for a pod", func() {
		It("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	When("type SRV DNS query for a non-existent service", func() {
		It("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})
}

func testClusterStatus() {
	var (
		rec *dnstest.Recorder
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockCs.clusterStatusMap[clusterID2] = true
		t.mockEs.endpointStatusMap[clusterID] = true
		t.mockEs.endpointStatusMap[clusterID2] = true

		t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2,
			portNumber2, protocol2, mcsv1a1.ClusterSetIP))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("service is in two clusters and specific cluster is requested", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID2, service1, namespace1)
		It("should succeed and write that cluster's IP as A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qtype: dns.TypeA,
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})

		It("should succeed and write that cluster's IP as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.lh.ServiceImports = setupServiceImportMap()
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2,
				portNumber2, protocol2, ""))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response with the available IP", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("should succeed and write that cluster's IP as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.mockCs.clusterStatusMap[clusterID] = false
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})
		It("should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.mockCs.clusterStatusMap[clusterID] = false
			t.mockCs.clusterStatusMap[clusterID2] = false
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty response (NODATA) for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should return empty response (NODATA) for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("service is present in one cluster and it is disconnected", func() {
		JustBeforeEach(func() {
			t.mockCs.clusterStatusMap[clusterID] = false
			delete(t.mockCs.clusterStatusMap, clusterID2)
			t.lh.ServiceImports = setupServiceImportMap()
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should return empty response (NODATA) for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should return empty response (NODATA) for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
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
		rec *dnstest.Recorder
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockCs.localClusterID = clusterID
		t.mockEs.endpointStatusMap[clusterID] = true
		t.mockEs.endpointStatusMap[clusterID2] = true

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("headless service has no IPs", func() {
		JustBeforeEach(func() {
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			t.lh.EndpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{}, []string{}, portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and return empty response (NODATA)", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
		It("should succeed and return empty response (NODATA)", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})
	When("headless service has one IP", func() {
		JustBeforeEach(func() {
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			t.lh.EndpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP},
				portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, endpointIP)),
				},
			})
		})
		It("should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1, portNumber1, protocol1,
				mcsv1a1.Headless))
			t.lh.EndpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1, hostName2},
				[]string{endpointIP, endpointIP2},
				portNumber1, protocol1))
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write two A records as response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID2, "", portName1,
				portNumber1, protocol1, mcsv1a1.Headless))
			t.lh.EndpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP},
				portNumber1, protocol1))
			t.lh.EndpointSlices.Put(newEndpointSlice(namespace1, service1, clusterID2, portName1, []string{hostName2}, []string{endpointIP2},
				portNumber1, protocol1))
			t.mockCs.clusterStatusMap[clusterID2] = true
		})
		When("no cluster is requested", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
			It("should succeed and write all IPs as A records in response", func() {
				t.executeTestCase(rec, test.Case{
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
				t.executeTestCase(rec, test.Case{
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
		rec *dnstest.Recorder
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockCs.clusterStatusMap[clusterID2] = true
		t.mockEs.endpointStatusMap[clusterID] = true
		t.mockEs.endpointStatusMap[clusterID2] = true
		t.mockCs.localClusterID = clusterID
		t.mockLs.LocalServicesMap[getKey(service1, namespace1)] = &serviceimport.DNSRecord{
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

		t.lh.ServiceImports.Put(newServiceImport(namespace1, service1, clusterID2, serviceIP2, portName2, portNumber2,
			protocol2, mcsv1a1.ClusterSetIP))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("service is in local and remote clusters", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write local cluster's IP as A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
			// Execute again to make sure not round robin
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})
		It("should succeed and write local cluster's IP as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})

		It("should succeed and write remote cluster's IP as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
			t.mockEs.endpointStatusMap[clusterID] = false
		})
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
		It("should succeed and write remote cluster's IP as A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
				},
			})
		})
		It("should succeed and write remote cluster's IP as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
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
		rec *dnstest.Recorder
		t   *handlerTestDriver
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.clusterStatusMap[clusterID] = true
		t.mockEs.endpointStatusMap[clusterID] = true
		t.mockCs.localClusterID = clusterID
		t.mockLs.LocalServicesMap[getKey(service1, namespace1)] = &serviceimport.DNSRecord{
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

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("DNS query of type SRV", func() {
		It("without portName should return all the ports", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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
			t.executeTestCase(rec, test.Case{
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

type handlerTestDriver struct {
	mockCs *MockClusterStatus
	mockEs *MockEndpointStatus
	mockLs *MockLocalServices
	lh     *lighthouse.Lighthouse
}

func newHandlerTestDriver() *handlerTestDriver {
	t := &handlerTestDriver{
		mockCs: NewMockClusterStatus(),
		mockEs: NewMockEndpointStatus(),
		mockLs: NewMockLocalServices(),
	}

	t.lh = &lighthouse.Lighthouse{
		Zones:           []string{"clusterset.local."},
		ServiceImports:  setupServiceImportMap(),
		EndpointSlices:  setupEndpointSliceMap(),
		ClusterStatus:   t.mockCs,
		EndpointsStatus: t.mockEs,
		LocalServices:   t.mockLs,
		TTL:             uint32(5),
	}

	return t
}

// nolint:gocritic // (hugeParam) It's fine to pass 'tc' by value here.
func (t *handlerTestDriver) executeTestCase(rec *dnstest.Recorder, tc test.Case) {
	code, err := t.lh.ServeDNS(context.TODO(), rec, tc.Msg())

	Expect(code).Should(Equal(tc.Rcode))

	if tc.Rcode == dns.RcodeSuccess {
		Expect(err).To(Succeed())
		Expect(test.SortAndCheck(rec.Msg, tc)).To(Succeed())
	} else {
		Expect(err).To(HaveOccurred())
	}
}

func setupServiceImportMap() *serviceimport.Map {
	siMap := serviceimport.NewMap(localClusterID)
	siMap.Put(newServiceImport(namespace1, service1, clusterID, serviceIP, portName1, portNumber1, protocol1, mcsv1a1.ClusterSetIP))

	return siMap
}

func setupEndpointSliceMap() *endpointslice.Map {
	esMap := endpointslice.NewMap()
	esMap.Put(newEndpointSlice(namespace1, service1, clusterID, portName1, []string{hostName1}, []string{endpointIP}, portNumber1, protocol1))

	return esMap
}

// nolint:unparam // `name` always receives `service1'.
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
				lhconstants.LighthouseLabelSourceCluster: clusterID,
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

// nolint:unparam // `namespace` always receives `namespace1`.
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
				discovery.LabelManagedBy:          lhconstants.LabelValueManagedBy,
				lhconstants.LabelSourceNamespace:  namespace,
				lhconstants.MCSLabelSourceCluster: clusterID,
				lhconstants.MCSLabelServiceName:   name,
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
