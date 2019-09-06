package lighthouse

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	mcservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
)

// ServeDNS implements the plugin.Handler interface.
func (lh *Lighthouse) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	log.Infof("Lighthouse plugin serving the DNS request")
	state := request.Request{W: w, Req: r}

	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true
	log.Infof("Request received for  %q ", state.QName())
	query := strings.Split(state.QName(), ".")
	svcName := query[0]
	nameSpace := query[1]
	service := lh.lookup(svcName, nameSpace)

	if service == nil || len(service.Spec.Items) == 0 {
		// We can't handle this,let another plugin take an attempt
		// NOTE: Once we have options enabled, this will only be done if
		//       fallthrough is enabled.
		return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, w, r)
	}
	serviceInfo := service.Spec.Items[0]
	rr := new(dns.A)

	if state.Family() == 1 {
		// IPv4 query
		rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass()}
		rr.A = net.ParseIP(serviceInfo.ServiceIP).To4()
	} else {
		// We don't support IPv6, let another plugin take an attempt
		log.Debugf("IPv6 queries not supported yet")
		return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, w, r)
	}

	a.Answer = []dns.RR{rr}

	log.Debugf("Responding to query with '%s'", a.Answer)
	wErr := w.WriteMsg(a)
	if wErr != nil {
		log.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, lh.Error("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (lh *Lighthouse) Error(str string) error {
	return plugin.Error(lh.Name(), errors.New(str))
}

// Name implements the Handler interface.
func (lh *Lighthouse) Name() string {
	return "lighthouse"
}
func (lh *Lighthouse) lookup(svcName string, nameSpace string) *mcservice.MultiClusterService {
	return lh.RemoteServiceMap[svcName+nameSpace]
}
