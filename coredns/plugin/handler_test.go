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
	k8snet "k8s.io/utils/net"
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
		Name:     "udp",
		Protocol: v1.ProtocolUDP,
		Port:     42,
	}

	port3 = mcsv1a1.ServicePort{
		Name:     "tcp",
		Protocol: v1.ProtocolTCP,
		Port:     42,
	}

	port4 = mcsv1a1.ServicePort{
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
	Context("Service with clusterset IP", testClusterSetIP)
	Context("IP families", testIPFamilies)
})

type FailingResponseWriter struct {
	test.ResponseWriter
	errorMsg string
}

func (w *FailingResponseWriter) WriteMsg(_ *dns.Msg) error {
	return errors.New(w.errorMsg)
}

func testWithoutFallback() {
	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))
	})

	Context("DNS query for an existing service", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("of Type A record should succeed and write an A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP)
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectIPResp(qname, serviceIP)
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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

			t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace2, service1, clusterID, []mcsv1a1.ServicePort{port1},
				newEndpoint(serviceIP, "", true)))
		})

		Specify("of Type A record should succeed and write an A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP)
		})

		Specify("of Type SRV should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNameError)
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNameError)
		})
	})

	Context("DNS query for a non-existent service with a different namespace", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace2)

		Specify("of Type A record should return RcodeNameError for A record query ", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNameError)
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query ", func() {
			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	Context("DNS query for a pod", func() {
		qname := fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1)

		Specify("of Type A record should return RcodeNameError for A record query", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNameError)
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	Context("DNS query for a non-existent zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace2)

		Specify("of Type A record should return RcodeNameError for A record query", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNotZone)
		})

		Specify("of Type SRV should return RcodeNameError for SRV record query", func() {
			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	Context("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("should return empty record", func() {
			t.executeTestCase(test.Case{
				Qname:  qname,
				Qtype:  dns.TypeAAAA,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("writing the response message fails", func() {
		BeforeEach(func() {
			t.rec = dnstest.NewRecorder(&FailingResponseWriter{errorMsg: "write failed"})
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should return error RcodeServerFailure", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeServerFailure)
		})
	})
}

func testWithFallback() {
	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)
		t.mockCs.SetLocalClusterID(clusterID)

		t.lh.Fall = fall.F{Zones: []string{"clusterset.local."}}
		t.lh.Next = test.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
			m := new(dns.Msg)
			m.SetRcode(r, dns.RcodeBadCookie)
			_ = w.WriteMsg(m)

			return dns.RcodeBadCookie, nil
		})

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))
	})

	Context("type A DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)

		Specify("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeBadCookie)
		})
	})

	Context("type A DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		qname := fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1)

		Specify("should not invoke the next plugin", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeNotZone)
		})
	})

	Context("type AAAA DNS query", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		Specify("should return empty record", func() {
			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeBadCookie)
		})
	})

	Context("type A DNS query for a non-existent service", func() {
		Specify("should invoke the next plugin", func() {
			t.execTypeAQueryExpectRespCode(fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1), dns.RcodeBadCookie)
		})
	})

	Context("type SRV DNS query for a non-matching lighthouse zone and matching fallthrough zone", func() {
		Specify("should invoke the next plugin", func() {
			t.lh.Fall = fall.F{Zones: []string{"clusterset.local.", "cluster.east."}}
			t.executeTestCase(test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type SRV DNS query for a non-matching lighthouse zone and non-matching fallthrough zone", func() {
		Specify("should not invoke the next plugin", func() {
			t.executeTestCase(test.Case{
				Qname: fmt.Sprintf("%s.%s.svc.cluster.east.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeNotZone,
			})
		})
	})

	Context("type SRV DNS query for a pod", func() {
		Specify("should invoke the next plugin", func() {
			t.executeTestCase(test.Case{
				Qname: fmt.Sprintf("%s.%s.pod.clusterset.local.", service1, namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})

	Context("type SRV DNS query for a non-existent service", func() {
		Specify("should invoke the next plugin", func() {
			t.executeTestCase(test.Case{
				Qname: fmt.Sprintf("unknown.%s.svc.clusterset.local.", namespace1),
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeBadCookie,
			})
		})
	})
}

func testClusterStatus() {
	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)
		t.mockCs.ConnectClusterID(clusterID2, k8snet.IPv4)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1, port2},
			newEndpoint(serviceIP2, "", true)))
	})

	When("a service is in two clusters and specific cluster is requested", func() {
		qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID2, service1, namespace1)

		It("should succeed and write that cluster's IP as A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP2)
		})

		It("should succeed and write that cluster's ports as SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.mockCs.DisconnectClusterID(clusterID, k8snet.IPv4)
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and write an A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP2)
		})

		It("should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.mockCs.DisconnectAll(k8snet.IPv4)
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should return empty response (NODATA) for A record query", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeSuccess)
		})

		It("should return empty response (NODATA) for SRV record query", func() {
			t.executeTestCase(test.Case{
				Qname:  qname,
				Qtype:  dns.TypeSRV,
				Rcode:  dns.RcodeSuccess,
				Answer: []dns.RR{},
			})
		})
	})

	When("a service is present in one cluster and it is disconnected", func() {
		JustBeforeEach(func() {
			t.mockCs.DisconnectClusterID(clusterID, k8snet.IPv4)

			t.lh.Resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{}))
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should return empty response (NODATA) for A record query", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeSuccess)
		})

		It("should return empty response (NODATA) for SRV record query", func() {
			t.executeTestCase(test.Case{
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
		t         *handlerTestDriver
		endpoints []discovery.Endpoint
	)

	BeforeEach(func() {
		endpoints = []discovery.Endpoint{}

		t = newHandlerTestDriver()

		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.Headless))
	})

	JustBeforeEach(func() {
		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1}, endpoints...))
	})

	When("a headless service has no endpoints", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and return empty response (NODATA)", func() {
			t.execTypeAQueryExpectRespCode(qname, dns.RcodeSuccess)
		})

		It("should succeed and return empty response (NODATA)", func() {
			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectIPResp(qname, endpointIP)
		})

		It("should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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

			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectIPResp(qname, endpointIP, endpointIP2)
		})

		It("should succeed and write an SRV record response", func() {
			t.executeTestCase(test.Case{
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

			t.executeTestCase(test.Case{
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

			t.executeTestCase(test.Case{
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

			t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1},
				newEndpoint(endpointIP2, hostName2, true)))

			endpoints = append(endpoints, newEndpoint(endpointIP, hostName1, true))

			t.mockCs.ConnectClusterID(clusterID2, k8snet.IPv4)
		})

		Context("and no cluster is requested", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

			It("should succeed and write all IPs as A records in response", func() {
				t.execTypeAQueryExpectIPResp(qname, endpointIP, endpointIP2)
			})
		})

		Context("and a specific clusteris requested", func() {
			qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)

			It("should succeed and write the cluster's IP as A record in response", func() {
				t.execTypeAQueryExpectIPResp(qname, endpointIP)
			})
		})
	})
}

func testLocalService() {
	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)
		t.mockCs.ConnectClusterID(clusterID2, k8snet.IPv4)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port1, port2},
			newEndpoint(serviceIP2, "", true)))
	})

	JustBeforeEach(func() {
		t.mockCs.SetLocalClusterID(clusterID)
	})

	When("a service is in local and remote clusters", func() {
		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and write the local cluster's IP as A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP)

			// Execute again to make sure no round robin
			t.execTypeAQueryExpectIPResp(qname, serviceIP)
		})

		It("should succeed and write the local cluster's port as SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.execTypeAQueryExpectIPResp(qname, serviceIP2)
		})

		It("should succeed and write the remote cluster's ports as SRV record response", func() {
			t.executeTestCase(test.Case{
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
			t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
				newEndpoint(serviceIP, "", false)))
		})

		qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

		It("should succeed and write the remote cluster's IP as A record response", func() {
			t.execTypeAQueryExpectIPResp(qname, serviceIP2)
		})
	})
}

func testSRVMultiplePorts() {
	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()
		t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)

		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1, port2, port3},
			newEndpoint(endpointIP, "", true)))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID2,
			[]mcsv1a1.ServicePort{port1, port2, port3, port4},
			newEndpoint(serviceIP2, "", true)))
	})

	Context("a DNS query of type SRV", func() {
		Specify("without a port name should return all the unique ports", func() {
			qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

			t.executeTestCase(test.Case{
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

			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, port1.Port, service1, namespace1)),
				},
			})

			qname = fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", port2.Name, port2.Protocol, service1, namespace1)

			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname, port2.Port, service1, namespace1)),
				},
			})

			qname = fmt.Sprintf("%s.%s.%s.%s.svc.clusterset.local.", port3.Name, port3.Protocol, service1, namespace1)

			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.svc.clusterset.local.", qname,
						port3.Port, service1, namespace1)),
				},
			})
		})

		Specify("with a DNS cluster name requested should return all the unique ports from the cluster", func() {
			qname := fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID, service1, namespace1)

			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})

			qname = fmt.Sprintf("%s.%s.%s.svc.clusterset.local.", clusterID2, service1, namespace1)

			t.executeTestCase(test.Case{
				Qname: qname,
				Qtype: dns.TypeSRV,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port4.Port, qname)),
					test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
				},
			})
		})

		Specify("with a port name requested with underscore prefix should return the port", func() {
			qname := fmt.Sprintf("_%s._%s.%s.%s.svc.clusterset.local.", port1.Name, port1.Protocol, service1, namespace1)

			t.executeTestCase(test.Case{
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

func testClusterSetIP() {
	const clusterSetIP = "243.1.0.1"

	qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

	var t *handlerTestDriver

	BeforeEach(func() {
		t = newHandlerTestDriver()

		si := newServiceImport(namespace1, service1, mcsv1a1.ClusterSetIP)
		si.Spec.IPs = []string{clusterSetIP}
		si.Spec.Ports = []mcsv1a1.ServicePort{port1, port2}

		t.lh.Resolver.PutServiceImport(si)

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(serviceIP, "", true)))

		t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID2, []mcsv1a1.ServicePort{port2},
			newEndpoint(serviceIP2, "", true)))
	})

	Specify("DNS query of Type A record should succeed and write an A record response", func() {
		t.execTypeAQueryExpectIPResp(qname, clusterSetIP)
	})

	Specify("DNS query of Type SRV should succeed and write an SRV record response", func() {
		t.executeTestCase(test.Case{
			Qname: qname,
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
				test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port1.Port, qname)),
			},
		})
	})
}

type handlerTestDriver struct {
	mockCs *fakecs.ClusterStatus
	lh     *lighthouse.Lighthouse
	rec    *dnstest.Recorder
}

func newHandlerTestDriver() *handlerTestDriver {
	t := &handlerTestDriver{
		mockCs: fakecs.NewClusterStatus(""),
		rec:    dnstest.NewRecorder(&test.ResponseWriter{}),
	}

	t.lh = &lighthouse.Lighthouse{
		Zones:               []string{"clusterset.local."},
		ClusterStatus:       t.mockCs,
		Resolver:            resolver.New(t.mockCs, fake.NewSimpleDynamicClient(scheme.Scheme)),
		TTL:                 uint32(5),
		SupportedIPFamilies: []k8snet.IPFamily{k8snet.IPv4},
	}

	return t
}

//nolint:gocritic // (hugeParam) It's fine to pass 'tc' by value here.
func (t *handlerTestDriver) executeTestCase(tc test.Case) {
	code, err := t.lh.ServeDNS(context.TODO(), t.rec, tc.Msg())

	if plugin.ClientWrite(tc.Rcode) {
		Expect(err).To(Succeed())
		Expect(test.SortAndCheck(t.rec.Msg, tc)).To(Succeed())
	} else {
		Expect(err).To(HaveOccurred())
		Expect(code).Should(Equal(tc.Rcode))
	}
}

func (t *handlerTestDriver) execTypeAQueryExpectIPResp(qname string, ips ...string) {
	answer := make([]dns.RR, len(ips))
	for i := range ips {
		answer[i] = test.A(fmt.Sprintf("%s    5    IN    A    %s", qname, ips[i]))
	}

	t.executeTestCase(test.Case{
		Qtype:  dns.TypeA,
		Qname:  qname,
		Rcode:  dns.RcodeSuccess,
		Answer: answer,
	})
}

func (t *handlerTestDriver) execTypeAQueryExpectRespCode(qname string, rcode int) {
	t.executeTestCase(test.Case{
		Qname: qname,
		Qtype: dns.TypeA,
		Rcode: rcode,
	})
}

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

func newEndpointSlice(namespace, name, clusterID string, ports []mcsv1a1.ServicePort, endpoints ...discovery.Endpoint,
) *discovery.EndpointSlice {
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
