package lighthouse

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

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
	if state.QType() != dns.TypeA && state.QType() != dns.TypeAAAA {
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

	query := strings.Split(qname, ".")
	svcName := query[0]
	namespace := query[1]

	var serviceIp string
	clusterInfo, found := lh.serviceImports.GetClusterInfo(namespace, svcName)
	for cluster := range lh.clusters.Get() {
		if val, ok := clusterInfo[cluster]; ok {
			log.Errorf("Found IP %v for connected cluster %q", val, cluster)
			serviceIp = val
			break
		}
	}
	if !found || serviceIp == "" {
		// We couldn't find record for this service name
		log.Debugf("No record found for service %q", qname)
		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNameError, "IP not found")
	}

	if state.QType() == dns.TypeAAAA {
		log.Debugf("Returning empty response for TypeAAAA query")
		return lh.emptyIpv6Response(state)
	}

	rr := new(dns.A)
	rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass()}
	rr.A = net.ParseIP(serviceIp).To4()

	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true
	a.Answer = []dns.RR{rr}

	log.Debugf("Responding to query with '%s'", a.Answer)
	wErr := w.WriteMsg(a)
	if wErr != nil {
		// Error writing reply msg
		log.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, lh.error("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (lh *Lighthouse) emptyIpv6Response(state request.Request) (int, error) {
	rr := new(dns.AAAA)
	rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeAAAA, Class: state.QClass()}
	rr.AAAA = net.IPv6unspecified

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

// Name implements the Handler interface.
func (lh *Lighthouse) Name() string {
	return "lighthouse"
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
