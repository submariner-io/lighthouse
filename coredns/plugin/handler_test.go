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

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/lighthouse/coredns/constants"
	lighthouse "github.com/submariner-io/lighthouse/coredns/plugin"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	fakecs "github.com/submariner-io/lighthouse/coredns/resolver/fake"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
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
	hostName1   = "hostName1"
	hostName2   = "hostName2"
)

var (
	port1 = mcsv1a1.ServicePort{
		Name:     "http",
		Protocol: v1.ProtocolTCP,
		Port:     8080,
	}

	port2 = mcsv1a1.ServicePort{
		Name:     "dns",
		Protocol: v1.ProtocolUDP,
		Port:     53,
	}
)

var _ = Describe("Lighthouse DNS plugin Handler", func() {
	Context("Fallthrough not configured", testWithoutFallback)
	Context("Fallthrough configured", testWithFallback)
	Context("Cluster connectivity status", testClusterStatus)
	Context("Headless services", testHeadlessService)
	Context("Local services", testLocalService)
	Context("Service with multiple ports", testSRVMultiplePorts)
})

type FailingResponseWriter struct {
	test.ResponseWriter
	errorMsg string
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
		t.mockCs.ConnectClusterID(clusterID)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	Context("DNS query for an existing service", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("of Type A record should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	Context("DNS query for an existing service in a specific cluster", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)

		Specify("of Type A record should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Qtype: dns.TypeA,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qtype: dns.TypeSRV,
				Qname: qname,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	Context("DNS query for an existing service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)

		BeforeEach(func() {
			t.lh.Resolver.PutServiceImport(newServiceImport(namespace2, service1, mcsv1a1.ClusterSetIP))

			t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace2, service1, clusterID, []mcsv1a1.ServicePort{port1},
				newEndpoint(serviceIP, "", true)))
		})

		Specify("of Type A record should succeed and write an A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	Context("DNS query for a non-existent service", func() {
		qname := fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1)

		Specify("of Type A record should return RcodeNameError for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	Context("DNS query for a non-existent service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)

		Specify("of Type A record should return RcodeNameError for A record query ", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query ", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	Context("DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)

		Specify("of Type A record should return RcodeNameError for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	Context("DNS query for a non-existent zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace2)

		Specify("of Type A record should return RcodeNameError for A record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	Context("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("should return empty record", func() {
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
		t.mockCs.ConnectClusterID(clusterID)
		t.mockCs.SetLocalClusterID(clusterID)

		t.lh.Fall = fall.F{Zones: []string{"clusterset.local."}}
		t.lh.Next = test.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
			m := new(dns.Msg)
			m.SetRcode(r, dns.RcodeBadCookie)
			_ = w.WriteMsg(m)
			return dns.RcodeBadCookie, nil
		})

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	Context("type A DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)

		Specify("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type A DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)

		Specify("should not invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	Context("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("should return empty record", func() {
			t.executeTestCase(rec, test.Case{
				Qname:  qname,
				Qtype:  dns.TypeAAAA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	Context("type A DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)

		Specify("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type A DNS query for a non-existent service", func() {
		Specify("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type SRV DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		Specify("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type SRV DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		Specify("should not invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	Context("type SRV DNS query for a pod", func() {
		Specify("should invoke the next plugin", func() {
			t.executeTestCase(rec, test.Case{
				Qname: fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type SRV DNS query for a non-existent service", func() {
		Specify("should invoke the next plugin", func() {
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
		t.mockCs.ConnectClusterID(clusterID)
		t.mockCs.ConnectClusterID(clusterID2)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1, port2},
			newEndpoint(serviceIP2, "", true)))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("a service is in two clusters and specific cluster is requested", func() {
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

		It("should succeed and write that cluster's ports as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	When("a service is in two clusters and only one is connected", func() {
		JustBeforeEach(func() {
			t.mockCs.DisconnectClusterID(clusterID)
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
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	When("a service is present in two clusters and both are disconnected", func() {
		JustBeforeEach(func() {
			t.mockCs.DisconnectAll()
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

	When("a service is present in one cluster and it is disconnected", func() {
		JustBeforeEach(func() {
			t.mockCs.DisconnectClusterID(clusterID)

			t.lh.Resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{}))
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
		rec       *dnstest.Recorder
		t         *handlerTestDriver
		endpoints []discovery.Endpoint
	)

	BeforeEach(func() {
		endpoints = []discovery.Endpoint{}

		t = newHandlerTestDriver()

		t.mockCs.ConnectClusterID(clusterID)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.Headless))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	JustBeforeEach(func() {
		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1}, endpoints...))
	})

	When("a headless service has no endpoints", func() {
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

	When("a headless service has one endpoint", func() {
		BeforeEach(func() {
			endpoints = append(endpoints, newEndpoint(endpointIP, hostName1, true))
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
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.%s", qname, port1.Port, hostName1, clusterID, qname)),
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
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s", qname, port1.Port, hostName1, qname)),
				},
			})
		})
	})

	When("headless service has two endpoints", func() {
		BeforeEach(func() {
			endpoints = append(endpoints, newEndpoint(endpointIP, hostName1, true), newEndpoint(endpointIP2, hostName2, true))
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
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s", qname, port1.Port, hostName1, clusterID, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s", qname, port1.Port, hostName2, clusterID, qname)),
				},
			})
		})

		It("should succeed and write an SRV record response when port and protocol is queried", func() {
			qname = fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", port1.Name, port1.Protocol, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, port1.Port, hostName1, clusterID, service1, namespace1)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, port1.Port, hostName2, clusterID, service1, namespace1)),
				},
			})
		})

		It("should succeed and write an SRV record response when port and protocol is queried with underscore prefix", func() {
			qname = fmt.Sprintf("_%s._%s.%s.%s.svc.clusterset.local.", port1.Name, port1.Protocol, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, port1.Port, hostName1, clusterID, service1, namespace1)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV  0 50 %d %s.%s.%s.%s.svc.clusterset.local.",
						qname, port1.Port, hostName2, clusterID, service1, namespace1)),
				},
			})
		})
	})

	When("headless service is present in two clusters", func() {
		BeforeEach(func() {
			t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.Headless))

			t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1},
				newEndpoint(endpointIP2, hostName2, true)))

			endpoints = append(endpoints, newEndpoint(endpointIP, hostName1, true))

			t.mockCs.ConnectClusterID(clusterID2)
		})

		Context("and no cluster is requested", func() {
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

		Context("and a specific clusteris requested", func() {
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
		t.mockCs.ConnectClusterID(clusterID)
		t.mockCs.ConnectClusterID(clusterID2)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1, port2},
			newEndpoint(serviceIP2, "", true)))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	JustBeforeEach(func() {
		t.mockCs.SetLocalClusterID(clusterID)
	})

	When("a service is in local and remote clusters", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and write the local cluster's IP as A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
			// Execute again to make sure no round robin
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP)),
				},
			})
		})

		It("should succeed and write the local cluster's port as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	When("a service is in local and remote clusters and the remote cluster is requested", func() {
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

		It("should succeed and write the remote cluster's ports as SRV record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})
	})

	When("service is in local and remote clusters and local has no active endpoints", func() {
		BeforeEach(func() {
			t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
				newEndpoint(serviceIP, "", false)))
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and write the remote cluster's IP as A record response", func() {
			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, serviceIP2)),
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
		t.mockCs.ConnectClusterID(clusterID)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlice(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1, port2},
			newEndpoint(endpointIP, "", true)))

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	Context("a DNS query of type SRV", func() {
		Specify("without a port name should return all the ports", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})

		Specify("with a port name requested should return only that port", func() {
			qname := fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", port1.Name, port1.Protocol, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, port1.Port, service1, namespace1)),
				},
			})

			qname = fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", port2.Name, port2.Protocol, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, port2.Port, service1, namespace1)),
				},
			})
		})

		Specify("with a DNS cluster name requested should return all the ports from the cluster", func() {
			qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})

		Specify("with a port name requested with underscore prefix should return the port", func() {
			qname := fmt.Sprintf("_%s._%s.%s.%s.svc.clusterset.local.", port1.Name, port1.Protocol, service1, namespace1)

			t.executeTestCase(rec, test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, port1.Port, service1, namespace1)),
				},
			})
		})
	})
}

type handlerTestDriver struct {
	mockCs *fakecs.ClusterStatus
	lh     *lighthouse.Lighthouse
}

func newHandlerTestDriver() *handlerTestDriver {
	t := &handlerTestDriver{
		mockCs: fakecs.NewClusterStatus(""),
	}

	t.lh = &lighthouse.Lighthouse{
		Zones:         []string{"clusterset.local."},
		ClusterStatus: t.mockCs,
		Resolver:      resolver.New(t.mockCs, fake.NewSimpleDynamicClient(scheme.Scheme)),
		TTL:           uint32(5),
	}

	return t
}

//nolint:gocritic // (hugeParam) It's fine to pass 'tc' by value here.
func (t *handlerTestDriver) executeTestCase(rec *dnstest.Recorder, tc test.Case) {
	code, err := t.lh.ServeDNS(context.TODO(), rec, tc.Msg())

	if plugin.ClientWrite(tc.Rcode) {
		Expect(err).To(Succeed())
		Expect(test.SortAndCheck(rec.Msg, tc)).To(Succeed())
	} else {
		Expect(err).To(HaveOccurred())
		Expect(code).Should(Equal(tc.Rcode))
	}
}

//nolint:unparam // `name` always receives `service1'.
func newServiceImport(namespace, name string, siType mcsv1a1.ServiceImportType) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type: siType,
		},
	}
}

//nolint:unparam // `namespace` always receives `namespace1`.
func newEndpointSlice(namespace, name, clusterID string, ports []mcsv1a1.ServicePort,
	endpoints ...discovery.Endpoint) *discovery.EndpointSlice {
	epPorts := make([]discovery.EndpointPort, len(ports))
	for i := range ports {
		epPorts[i] = discovery.EndpointPort{
			Name:        &ports[i].Name,
			Protocol:    &ports[i].Protocol,
			Port:        &ports[i].Port,
			AppProtocol: ports[i].AppProtocol,
		}
	}

	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: namespace,
			Labels: map[string]string{
				discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				constants.LabelSourceNamespace:  namespace,
				constants.MCSLabelSourceCluster: clusterID,
				mcsv1a1.LabelServiceName:        name,
				constants.LabelIsHeadless:       "false",
			},
		},
		AddressType: discovery.AddressTypeIPv4,
		Ports:       epPorts,
		Endpoints:   endpoints,
	}
}

func newEndpoint(address, hostname string, ready bool) discovery.Endpoint {
	return discovery.Endpoint{
		Addresses:  []string{address},
		Hostname:   &hostname,
		Conditions: discovery.EndpointConditions{Ready: &ready},
	}
}
