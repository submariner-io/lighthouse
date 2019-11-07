package lighthouse

import (
	"context"
	"errors"
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

	if state.QType() != dns.TypeA {
		// We only support TypeA
		log.Debugf("Only TypeA queries are supported yet")
		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNotImplemented, "Only TypeA supported")
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
	service, found := lh.multiClusterServices.get(namespace, svcName)

	if !found || len(service.Spec.Items) == 0 {
		// We couldn't find record for this service name
		log.Debugf("No record found for service %q", qname)
		return lh.nextOrFailure(state.Name(), ctx, w, r, dns.RcodeNameError, "IP not found")
	}

	serviceInfo := service.Spec.Items[0]

	rr := new(dns.A)
	rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass()}
	rr.A = net.ParseIP(serviceInfo.ServiceIP).To4()

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

// Name implements the Handler interface.
func (lh *Lighthouse) Name() string {
	return "lighthouse"
}

func (lh *Lighthouse) error(str string) error {
	return plugin.Error(lh.Name(), errors.New(str))
}

func (lh *Lighthouse) nextOrFailure(name string, ctx context.Context, w dns.ResponseWriter, r *dns.Msg, code int, error string) (int, error) {
	if lh.Fall.Through(name) {
		return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, w, r)
	} else {
		return code, lh.error(error)
	}
}
