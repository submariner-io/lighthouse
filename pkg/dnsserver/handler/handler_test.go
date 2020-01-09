package handler

import (
	"testing"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	v1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	"github.com/submariner-io/lighthouse/pkg/multiclusterservice"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

const (
	service1   = "service1"
	namespace1 = "namespace1"
	namespace2 = "namespace2"
	serviceIP1 = "100.96.156.175"
	serviceIP2 = "101.96.156.175"
)

type TestResponseWriter struct {
	test.ResponseWriter
	capturedMsg *dns.Msg
}

func (w *TestResponseWriter) WriteMsg(m *dns.Msg) error {
	w.capturedMsg = m
	return nil
}

var _ = Describe("ServeDNS function", func() {
	klog.InitFlags(nil)

	var (
		handler              dns.Handler
		multiClusterServices *multiclusterservice.Map
	)

	BeforeEach(func() {
		multiClusterServices = new(multiclusterservice.Map)
		multiClusterServices.Put(newMultiClusterService(namespace1, service1, serviceIP1, "clusterID"))
	})

	JustBeforeEach(func() {
		handler = New(multiClusterServices)
	})

	When("type A DNS query for an existing service", func() {
		It("should succeed and write an A record response", func() {
			executeTestCase(handler, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.cluster.local.    0    IN    A    " + serviceIP1),
				},
			})
		})
	})

	When("type A DNS query for an existing service with a different namespace", func() {
		BeforeEach(func() {
			multiClusterServices.Put(newMultiClusterService(namespace2, service1, serviceIP2, "clusterID"))
		})

		It("should succeed and write an A record response", func() {
			executeTestCase(handler, test.Case{
				Qname: service1 + "." + namespace2 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace2 + ".svc.cluster.local.    0    IN    A    " + serviceIP2),
				},
			})
		})
	})

	When("type A DNS query for a non-existent service", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(handler, test.Case{
				Qname: "unknown." + namespace1 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("type A DNS query for a non-existent service with a different namespace", func() {
		It("should return RcodeNameError", func() {
			executeTestCase(handler, test.Case{
				Qname: service1 + "." + namespace2 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			})
		})
	})

	When("type AAAA DNS query", func() {
		It("should return error RcodeNotImplemented", func() {
			executeTestCase(handler, test.Case{
				Qname: service1 + "." + namespace1 + ".svc.cluster.local.",
				Qtype: dns.TypeAAAA,
				Rcode: dns.RcodeNotImplemented,
			})
		})
	})
})

func executeTestCase(handler dns.Handler, tc test.Case) {
	writer := &TestResponseWriter{}
	handler.ServeDNS(writer, tc.Msg())

	Expect(writer.capturedMsg.Rcode).Should(Equal(tc.Rcode))
	if tc.Rcode == dns.RcodeSuccess {
		Expect(test.SortAndCheck(writer.capturedMsg, tc)).To(Succeed())
	}
}

func newMultiClusterService(namespace, name, serviceIP, clusterID string) *v1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				lighthousev1.ClusterServiceInfo{
					ClusterID: clusterID,
					ServiceIP: serviceIP,
				},
			},
		},
	}
}

func TestDNSServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DNS Request Handler")
}
