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
	"slices"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/submariner-io/admiral/pkg/log"
	k8snet "k8s.io/utils/net"
)

const PluginName = "lighthouse"

// ServeDNS implements the plugin.Handler interface.
func (lh *Lighthouse) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := &request.Request{W: w, Req: r}
	qname := state.QName()

	logger.V(log.TRACE).Infof("Request received for %q, type: %v", qname, state.QType())

	// qname: mysvc.default.svc.example.org.
	// zone:  example.org.
	// Matches will return zone in all lower cases
	zone := plugin.Zones(lh.Zones).Matches(qname)
	if zone == "" {
		logger.V(log.TRACE).Infof("Request does not match configured zones %v", lh.Zones)
		return lh.nextOrFailure(ctx, state, r, dns.RcodeNotZone)
	}

	if state.QType() != dns.TypeA && state.QType() != dns.TypeAAAA && state.QType() != dns.TypeSRV {
		logger.V(log.TRACE).Infof("Query of type %d is not supported", state.QType())

		return lh.nextOrFailure(ctx, state, r, dns.RcodeNotImplemented)
	}

	zone = qname[len(qname)-len(zone):] // maintain case of original query
	state.Zone = zone

	pReq, pErr := parseRequest(state)
	if pErr != nil || pReq.podOrSvc != Svc {
		// We only support svc type queries i.e. *.svc.*
		logger.V(log.TRACE).Infof("Request type %q is not a 'svc' type query - err was %v", pReq.podOrSvc, pErr)
		return lh.nextOrFailure(ctx, state, r, dns.RcodeNameError)
	}

	return lh.getDNSRecord(ctx, zone, state, w, r, pReq)
}

func (lh *Lighthouse) getDNSRecord(ctx context.Context, zone string, state *request.Request, w dns.ResponseWriter,
	r *dns.Msg, pReq *recordRequest,
) (int, error) {
	ipFamily := k8snet.IPFamilyUnknown
	if state.QType() == dns.TypeA {
		ipFamily = k8snet.IPv4
	} else if state.QType() == dns.TypeAAAA {
		ipFamily = k8snet.IPv6
	}

	if ipFamily != k8snet.IPFamilyUnknown && !slices.Contains(lh.SupportedIPFamilies, ipFamily) {
		logger.V(log.TRACE).Infof("IPv%s records not supported", ipFamily)
		return lh.emptyResponse(state)
	}

	dnsRecords, isHeadless, found := lh.Resolver.GetDNSRecords(pReq.namespace, pReq.service, pReq.cluster, pReq.hostname, ipFamily)
	if !found {
		logger.V(log.TRACE).Infof("No record found for %q", state.QName())
		return lh.nextOrFailure(ctx, state, r, dns.RcodeNameError)
	}

	if len(dnsRecords) == 0 {
		logger.V(log.TRACE).Infof("Couldn't find a connected cluster or valid IPs for %q", state.QName())
		return lh.emptyResponse(state)
	}

	// Count records
	localClusterID := lh.ClusterStatus.GetLocalClusterID()
	for _, record := range dnsRecords {
		incDNSQueryCounter(localClusterID, record.ClusterName, pReq.service, pReq.namespace, record.IP)
	}

	records := make([]dns.RR, 0)

	switch state.QType() {
	case dns.TypeA:
		records = lh.createARecords(dnsRecords, state)
	case dns.TypeAAAA:
		records = lh.createAAAARecords(dnsRecords, state)
	case dns.TypeSRV:
		records = lh.createSRVRecords(dnsRecords, state, pReq, zone, isHeadless)
	}

	if len(records) == 0 {
		logger.V(log.TRACE).Infof("Couldn't find a connected cluster or valid record for %q", state.QName())
		return lh.emptyResponse(state)
	}

	logger.V(log.TRACE).Infof("rr is %v", records)

	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true
	a.Answer = append(a.Answer, records...)

	logger.V(log.TRACE).Infof("Responding to query with '%s'", a.Answer)

	wErr := w.WriteMsg(a)
	if wErr != nil {
		// Error writing reply msg
		logger.Errorf(wErr, "Failed to write message %#v", a)
		return dns.RcodeServerFailure, lh.error("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (lh *Lighthouse) emptyResponse(state *request.Request) (int, error) {
	a := new(dns.Msg)
	a.SetReply(state.Req)

	return lh.writeResponse(state, a)
}

func (lh *Lighthouse) writeResponse(state *request.Request, a *dns.Msg) (int, error) {
	a.Authoritative = true

	wErr := state.W.WriteMsg(a)
	if wErr != nil {
		logger.Errorf(wErr, "Failed to write message %#v", a)
		return dns.RcodeServerFailure, lh.error("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (lh *Lighthouse) Name() string {
	return PluginName
}

func (lh *Lighthouse) error(str string) error {
	return plugin.Error(lh.Name(), errors.New(str)) //nolint:wrapcheck // Let the caller wrap it.
}

func (lh *Lighthouse) nextOrFailure(ctx context.Context, state *request.Request, r *dns.Msg, rcode int) (int, error) {
	if lh.Fall.Through(state.Name()) {
		return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, state.W, r) //nolint:wrapcheck // Let the caller wrap it.
	}

	a := new(dns.Msg)
	a.SetRcode(r, rcode)

	return lh.writeResponse(state, a)
}
