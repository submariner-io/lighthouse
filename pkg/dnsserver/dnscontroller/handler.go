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
		klog.Infof("Lighthouse addressing DNS request for Domain %q", domain)
		query := strings.Split(domain, ".")
		svcName := query[0]
		namespace := query[1]
		klog.Infof("The service name %q, and namespace %q", svcName, namespace)
		service, found := lh.MultiClusterServices.Get(namespace, svcName)
		if !found || len(service.Spec.Items) == 0 {
			klog.Infof("No record found for service %q", domain)
			msg.Rcode = dns.RcodeNameError
		} else {
			serviceInfo := service.Spec.Items[0]
			klog.Infof("Record found for DNS request for %q and the clusterIP is %q",
				domain, serviceInfo.ServiceIP)
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(serviceInfo.ServiceIP),
			})
		}
	}
	err := w.WriteMsg(&msg)
	if err != nil {
		klog.Errorf("Failed to write the DNS replay message due to %q", err)
	}
}
