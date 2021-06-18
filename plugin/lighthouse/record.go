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
	"net"
	"strings"

	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (lh *Lighthouse) createARecords(dnsrecords []serviceimport.DNSRecord, state request.Request) []dns.RR {
	records := make([]dns.RR, 0)

	for _, record := range dnsrecords {
		dnsRecord := &dns.A{Hdr: dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass(),
			Ttl: lh.ttl}, A: net.ParseIP(record.IP).To4()}
		records = append(records, dnsRecord)
	}

	return records
}

func (lh *Lighthouse) createSRVRecords(dnsrecords []serviceimport.DNSRecord, state request.Request, pReq recordRequest, zone string,
	isHeadless bool) []dns.RR {
	var records []dns.RR

	for _, dnsRecord := range dnsrecords {
		var reqPorts []v1alpha1.ServicePort

		if pReq.port == "" {
			reqPorts = dnsRecord.Ports
		} else {
			log.Debugf("Requested port %q, protocol %q for SRV", pReq.port, pReq.protocol)
			for _, port := range dnsRecord.Ports {
				name := strings.ToLower(port.Name)
				protocol := strings.ToLower(string(port.Protocol))

				log.Debugf("Checking port %q, protocol %q", name, protocol)
				if name == pReq.port && protocol == pReq.protocol {
					reqPorts = append(reqPorts, port)
				}
			}
		}

		if len(reqPorts) == 0 {
			return nil
		}

		target := pReq.service + "." + pReq.namespace + ".svc." + zone

		if isHeadless {
			target = dnsRecord.ClusterName + "." + target
		} else if pReq.cluster != "" {
			target = pReq.cluster + "." + target
		}

		if isHeadless {
			target = dnsRecord.HostName + "." + target
		}

		for _, port := range reqPorts {
			record := &dns.SRV{
				Hdr:      dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeSRV, Class: state.QClass(), Ttl: lh.ttl},
				Priority: 0,
				Weight:   50,
				Port:     uint16(port.Port),
				Target:   target,
			}
			records = append(records, record)
		}
	}

	return records
}

func (lh *Lighthouse) getClusterIPForSvc(pReq recordRequest) (*serviceimport.DNSRecord, bool) {
	localClusterID := lh.clusterStatus.LocalClusterID()

	record, found, isLocal := lh.serviceImports.GetIP(pReq.namespace, pReq.service, pReq.cluster, localClusterID, lh.clusterStatus.IsConnected,
		lh.endpointsStatus.IsHealthy)
	getLocal := isLocal || (pReq.cluster != "" && pReq.cluster == localClusterID)
	if found && getLocal {
		record, found = lh.localServices.GetIP(pReq.service, pReq.namespace)
	}

	return record, found
}
