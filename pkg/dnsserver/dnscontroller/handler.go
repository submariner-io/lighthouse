package dnscontroller

import (
	"net"
	"strings"

	"k8s.io/klog"

	"github.com/miekg/dns"
)

func (lh Lighthouse) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	klog.Errorf("Serving  DNSRequest")
	msg := dns.Msg{}
	msg.SetReply(r)
	switch r.Question[0].Qtype {
	case dns.TypeA:
		msg.Authoritative = true
		domain := msg.Question[0].Name
		klog.Errorf("Domain %q", domain)
		query := strings.Split(domain, ".")
		svcName := query[0]
		namespace := query[1]
		klog.Errorf("svcName %q, namespace %q", svcName, namespace)
		service, found := lh.MultiClusterServices.get(namespace, svcName)
		if !found || len(service.Spec.Items) == 0 {
			// We couldn't find record for this service name
			klog.Errorf("No record found for service %q", domain)
			msg.Rcode = dns.RcodeNameError
		} else {
			klog.Errorf("Getting service Info %q", domain)
			serviceInfo := service.Spec.Items[0]
			klog.Errorf("Found service Info %q", domain)
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(serviceInfo.ServiceIP),
			})
		}
	}
	w.WriteMsg(&msg)
}
