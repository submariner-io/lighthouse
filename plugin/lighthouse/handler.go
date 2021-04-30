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
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/submariner-io/lighthouse/pkg/serviceimport"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const PluginName = "lighthouse"

// ServeDNS implements the plugin.Handler interface.
func (lh *Lighthouse) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := state.QName()

	log.Debugf("Request received for %q", qname)

	// qname: mysvc.default.svc.example.org.
	// zone:  example.org.
	// Matches will return zone in all lower cases
	zone := plugin.Zones(lh.Zones).Matches(qname)
	if zone == "" {
		log.Debugf("Request does not match configured zones %v", lh.Zones)
		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNotZone, "No matching zone found")
	}

	if state.QType() != dns.TypeA && state.QType() != dns.TypeAAAA && state.QType() != dns.TypeSRV {
		msg := fmt.Sprintf("Query of type %d is not supported", state.QType())
		log.Debugf(msg)

		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNotImplemented, msg)
	}

	zone = qname[len(qname)-len(zone):] // maintain case of original query
	state.Zone = zone

	pReq, pErr := parseRequest(state)
	if pErr != nil || pReq.podOrSvc != Svc {
		// We only support svc type queries i.e. *.svc.*
		log.Debugf("Request type %q is not a 'svc' type query - err was %v", pReq.podOrSvc, pErr)
		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNameError, "Only services supported")
	}

	return lh.getDNSRecord(zone, state, ctx, w, r, pReq)
}

func (lh *Lighthouse) createARecords(ips []string, state request.Request) []dns.RR {
	records := make([]dns.RR, 0)

	for _, ip := range ips {
		record := &dns.A{Hdr: dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass(), Ttl: lh.ttl}, A: net.ParseIP(ip).To4()}
		log.Debugf("rr is %v", record)
		records = append(records, record)
	}

	return records
}

func (lh *Lighthouse) createSRVRecords(record *serviceimport.DNSRecord, state request.Request, pReq recordRequest, zone string) []dns.RR {
	reqPorts := make([]mcsv1a1.ServicePort, 0)
	records := make([]dns.RR, 0)
	portReqKey := pReq.port + pReq.protocol
	if portReqKey == "" {
		reqPorts = record.Port
	}

	for _, port := range record.Port {
		portKey := strings.ToLower(port.Name) + strings.ToLower(string(port.Protocol))
		log.Debugf("The current port Name %q SRV require port Name %q", portKey, portReqKey)

		if portKey == portReqKey {
			reqPorts = append(reqPorts, port)
		}
	}

	if len(reqPorts) == 0 {
		return nil
	}

	target := pReq.service + "." + pReq.namespace + "." + zone

	if pReq.cluster != "" {
		target = pReq.cluster + "." + target
	}

	for _, ports := range reqPorts {
		record := &dns.SRV{
			Hdr:      dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeSRV, Class: state.QClass(), Ttl: lh.ttl},
			Priority: 0,
			Weight:   50,
			Port:     uint16(ports.Port),
			Target:   target,
		}
		records = append(records, record)
	}

	return records
}

func (lh *Lighthouse) getDNSRecord(zone string, state request.Request, ctx context.Context, w dns.ResponseWriter,
	r *dns.Msg, pReq recordRequest) (int, error) {
	var (
		ips    []string
		found  bool
		record *serviceimport.DNSRecord
	)

	record, found = lh.getClusterIPForSvc(pReq)
	if !found {
		ips, found = lh.endpointSlices.GetIPs(pReq.hostname, pReq.cluster, pReq.namespace, pReq.service, lh.clusterStatus.IsConnected)
		if !found {
			log.Debugf("No record found for %q", state.QName())
			return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNameError, "record not found")
		}
	} else if record != nil && record.IP != "" {
		ips = []string{record.IP}
	}

	if len(ips) == 0 {
		log.Debugf("Couldn't find a connected cluster or valid IPs for %q", state.QName())
		return lh.emptyResponse(state)
	}

	if state.QType() == dns.TypeAAAA {
		log.Debugf("Returning empty response for TypeAAAA query")
		return lh.emptyResponse(state)
	}

	records := make([]dns.RR, 0)

	if state.QType() == dns.TypeA {
		records = lh.createARecords(ips, state)
	} else if state.QType() == dns.TypeSRV {
		records = lh.createSRVRecords(record, state, pReq, zone)
		if records == nil {
			log.Debugf("Couldn't find a connected cluster or valid record for %q", state.QName())
			return lh.emptyResponse(state)
		}
	}

	log.Debugf("rr is %v", records)

	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true
	a.Answer = append(a.Answer, records...)
	log.Debugf("Responding to query with '%s'", a.Answer)

	wErr := w.WriteMsg(a)
	if wErr != nil {
		// Error writing reply msg
		log.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, lh.error("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (lh *Lighthouse) emptyResponse(state request.Request) (int, error) {
	a := new(dns.Msg)
	a.SetReply(state.Req)
	a.Authoritative = true

	wErr := state.W.WriteMsg(a)
	if wErr != nil {
		// Error writing reply msg
		log.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, lh.error("failed to write response")
	}

	return dns.RcodeSuccess, nil
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

// Name implements the Handler interface.
func (lh *Lighthouse) Name() string {
	return PluginName
}

func (lh *Lighthouse) error(str string) error {
	return plugin.Error(lh.Name(), errors.New(str))
}

func (lh *Lighthouse) nextOrFailure(name string, ctx context.Context, w dns.ResponseWriter, r *dns.Msg, code int, err string) (int, error) {
	if lh.Fall.Through(name) {
		return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, w, r)
	} else {
		return code, lh.error(err)
	}
}
