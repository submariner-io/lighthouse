package lighthouse

import (
	"context"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	service1   = "service1"
	namespace1 = "namespace1"
	serviceIP  = "100.96.156.175"
)

var _ = Describe("Lighthouse DNS plugin Handler", func() {
	Context("Fallthrough is not configured", testDnsServer)
	Context("Fallthrough is configured", testNext)
})

type FailingResponseWriter struct {
	test.ResponseWriter
	errorMsg string
}

func (w *FailingResponseWriter) WriteMsg(m *dns.Msg) error {
	return errors.New(w.errorMsg)
}

func testDnsServer() {
	var (
		rec *dnstest.Recorder
		lh  *Lighthouse
		tc  test.Case
	)

	BeforeEach(func() {
		lh = &Lighthouse{
			Zones:                []string{"cluster.local."},
			multiClusterServices: setupMultiClusterServiceMap(),
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("TypeA DNS query for service that exists", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service is present.
				Qname: service1 + "." + namespace1 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A(service1 + "." + namespace1 + ".svc.cluster.local.    0    IN    A    " + serviceIP),
				},
			}
		})
		It("Should return A record", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).NotTo(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
			respErr := test.SortAndCheck(rec.Msg, tc)
			Expect(respErr).NotTo(HaveOccurred())
		})
	})

	When("TypeA DNS query for service that doesn't exist", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc2.default.svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			}
		})
		It("Should return RcodeNameError", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("Type A DNS query for pod", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc2.default.pod.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			}
		})
		It("Should return RcodeNameError", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("Type AAAA DNS query", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc.default.svc.cluster.local.",
				Qtype: dns.TypeAAAA,
				Rcode: dns.RcodeNotImplemented,
			}
		})
		It("Should return error RcodeNotImplemented", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("WriteMsg fails", func() {
		BeforeEach(func() {
			tc = test.Case{
				Qname: service1 + "." + namespace1 + ".svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeServerFailure,
			}
			lh.Zones = []string{"cluster.local."}
			rec = dnstest.NewRecorder(&FailingResponseWriter{errorMsg: "write failed"})
		})

		It("Should return error RcodeServerFailure", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})
}

func testNext() {
	var (
		rec *dnstest.Recorder
		lh  *Lighthouse
		tc  test.Case
	)

	BeforeEach(func() {
		lh = &Lighthouse{
			Fall:                 fall.F{Zones: []string{"cluster.local."}},
			Next:                 test.NextHandler(dns.RcodeBadCookie, errors.New("dummy plugin")),
			multiClusterServices: setupMultiClusterServiceMap(),
		}

		rec = dnstest.NewRecorder(&test.ResponseWriter{})
	})

	When("Zones not configured", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc2.default.svc.cluster.local.", Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			}
		})
		It("Should call next plugin", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("Type AAAA DNS query", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc.default.svc.cluster.local.", Qtype: dns.TypeAAAA,
				Rcode: dns.RcodeBadCookie,
			}
			lh.Zones = []string{"cluster.local."}
		})
		It("Should call next plugin", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("Type A DNS query for pod", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc.default.pod.cluster.local.", Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			}
			lh.Zones = []string{"cluster.local."}
		})
		It("Should call next plugin", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("DNS query for service that doesn't exist", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc2.default.svc.cluster.local.", Qtype: dns.TypeA,
				Rcode: dns.RcodeBadCookie,
			}
			lh.Zones = []string{"cluster.local."}
		})
		It("Should call next plugin", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})
}

func setupMultiClusterServiceMap() *multiClusterServiceMap {
	mcsMap := new(multiClusterServiceMap)
	mcsMap.put(&lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service1,
			Namespace: namespace1,
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				lighthousev1.ClusterServiceInfo{
					ClusterID: "clusterID",
					ServiceIP: serviceIP,
				},
			},
		},
	})

	return mcsMap
}
