package handler

import (
	"net"
	"strings"

	"github.com/miekg/dns"
	"github.com/submariner-io/lighthouse/pkg/multiclusterservice"
	"k8s.io/klog"
)

type handler struct {
	multiClusterServices *multiclusterservice.Map
}

func New(multiClusterServices *multiclusterservice.Map) dns.Handler {
	return &handler{multiClusterServices: multiClusterServices}
}

func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	msg := dns.Msg{}
	msg.SetReply(r)

	question := r.Question[0]
	switch question.Qtype {
	case dns.TypeA:
		msg.Authoritative = true

		domain := question.Name
		query := strings.Split(domain, ".")
		svcName := query[0]
		namespace := query[1]
		klog.Infof("Serving DNS request for domain %q, service name %q, namespace %q", domain, svcName, namespace)

		service, found := h.multiClusterServices.Get(namespace, svcName)
		if !found || len(service.IpList) == 0 {
			klog.Infof("No record found for service %q", svcName)
			msg.Rcode = dns.RcodeNameError
		} else {
			serviceIp := service.IpList[0]
			klog.Infof("Record IP %q found for service %q", serviceIp, svcName)

			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET},
				A:   net.ParseIP(serviceIp),
			})
		}
	default:
		klog.Infof("DNS Request type %d is not supported", question.Qtype)
		msg.Rcode = dns.RcodeNotImplemented
	}

	err := w.WriteMsg(&msg)
	if err != nil {
		klog.Errorf("Error writing the DNS reply message: %v", err)
	}
}
