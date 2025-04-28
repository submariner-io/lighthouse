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
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo/v2"
	discovery "k8s.io/api/discovery/v1"
	k8snet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	ipv6ServiceIP = "fc00:2001::6757"
)

func testIPFamilies() {
	qname := fmt.Sprintf("%s.%s.svc.clusterset.local.", service1, namespace1)

	var (
		t                 *handlerTestDriver
		serviceImportType mcsv1a1.ServiceImportType
	)

	BeforeEach(func() {
		t = newHandlerTestDriver()

		serviceImportType = mcsv1a1.ClusterSetIP
	})

	JustBeforeEach(func() {
		t.lh.Resolver.PutServiceImport(newServiceImport(namespace1, service1, serviceImportType))
	})

	newIPv6EndpointSlice := func() *discovery.EndpointSlice {
		eps := newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port1},
			newEndpoint(ipv6ServiceIP, "", true))
		eps.AddressType = discovery.AddressTypeIPv6

		return eps
	}

	executeTypeAAAAQuery := func() {
		t.executeTestCase(test.Case{
			Qname: qname,
			Qtype: dns.TypeAAAA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.AAAA(fmt.Sprintf("%s    5    IN    AAAA    %s", qname, ipv6ServiceIP)),
			},
		})
	}

	testIPV6Only := func(serviceType mcsv1a1.ServiceImportType) {
		When("only IPv6 is supported locally", func() {
			BeforeEach(func() {
				serviceImportType = serviceType
			})

			JustBeforeEach(func() {
				t.lh.SupportedIPFamilies = []k8snet.IPFamily{k8snet.IPv6}

				t.mockCs.ConnectClusterID(clusterID, k8snet.IPv6)
				t.lh.Resolver.PutEndpointSlices(newIPv6EndpointSlice())
			})

			Context(fmt.Sprintf("for an IPv6 %s service", serviceType), func() {
				Specify("a Type AAAA DNS query should succeed and write a AAAA record response", func() {
					executeTypeAAAAQuery()
				})

				Specify("a Type A DNS query should return an empty record", func() {
					t.execTypeAQueryExpectRespCode(qname, dns.RcodeSuccess)
				})
			})

			Context(fmt.Sprintf("for a dual-stack %s service", serviceType), func() {
				JustBeforeEach(func() {
					t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port2},
						newEndpoint(serviceIP, "", true)))
				})

				Specify("a Type AAAA DNS query should succeed and write a AAAA record response", func() {
					executeTypeAAAAQuery()
				})

				Specify("a Type A DNS query should return an empty record", func() {
					t.execTypeAQueryExpectRespCode(qname, dns.RcodeSuccess)
				})
			})
		})
	}

	testIPV6Only(mcsv1a1.ClusterSetIP)
	testIPV6Only(mcsv1a1.Headless)

	testDualStack := func(serviceType mcsv1a1.ServiceImportType) {
		When("dual-stack is supported locally", func() {
			BeforeEach(func() {
				serviceImportType = serviceType
			})

			JustBeforeEach(func() {
				t.lh.SupportedIPFamilies = []k8snet.IPFamily{k8snet.IPv6, k8snet.IPv4}
				t.mockCs.ConnectClusterID(clusterID, k8snet.IPv6)
				t.mockCs.ConnectClusterID(clusterID, k8snet.IPv4)

				t.lh.Resolver.PutEndpointSlices(newIPv6EndpointSlice())
				t.lh.Resolver.PutEndpointSlices(newEndpointSlice(namespace1, service1, clusterID, []mcsv1a1.ServicePort{port2},
					newEndpoint(serviceIP, "", true)))
			})

			Context(fmt.Sprintf("for a dual-stack %s service", serviceType), func() {
				Specify("a Type AAAA DNS query should succeed and write a AAAA record response", func() {
					executeTypeAAAAQuery()
				})

				Specify("a Type A DNS query should succeed and write an A record response", func() {
					t.execTypeAQueryExpectIPResp(qname, serviceIP)
				})

				Specify("a Type SRV query should succeed and write an SRV record response", func() {
					answer := []dns.RR{
						test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s", qname, port2.Port, qname)),
					}

					if serviceType == mcsv1a1.Headless {
						answer = []dns.RR{
							test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.%s", qname, port2.Port,
								strings.ReplaceAll(serviceIP, ".", "-"), clusterID, qname)),
							test.SRV(fmt.Sprintf("%s    5    IN    SRV 0 50 %d %s.%s.%s", qname, port1.Port,
								strings.ReplaceAll(ipv6ServiceIP, ":", "-"), clusterID, qname)),
						}
					}

					t.executeTestCase(test.Case{
						Qname:  qname,
						Qtype:  dns.TypeSRV,
						Rcode:  dns.RcodeSuccess,
						Answer: answer,
					})
				})
			})
		})
	}

	testDualStack(mcsv1a1.ClusterSetIP)
	testDualStack(mcsv1a1.Headless)
}
