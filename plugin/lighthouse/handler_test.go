package lighthouse

import (
	"context"
	"os"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/pkg/errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[handler] Test basic DNS Handler", func() {
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
		lh  *Lighthouse
		ctx context.Context
		tc  test.Case
	)

	os.Setenv("LIGHTHOUSE_SVCS", "mysvc=100.96.156.175,dummy=100.96.156.175,none=100.96.156.100")
	lh = &Lighthouse{}
	lh.SvcsMap = setupServicesMap()
	lh.Zones = []string{"cluster.local."}
	rec := dnstest.NewRecorder(&test.ResponseWriter{})

	ctx = context.TODO()

	When("TypeA DNS query for service that exists", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service is present.
				Qname: "mysvc.default.svc.cluster.local.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("mysvc.default.svc.cluster.local.    0    IN    A    100.96.156.175"),
				},
			}
		})
		It("Should return A record", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})
}

func testNext() {
	var (
		lh  *Lighthouse
		ctx context.Context
		tc  test.Case
	)

	os.Setenv("LIGHTHOUSE_SVCS", "mysvc=100.96.156.175,dummy=100.96.156.175,none=100.96.156.100")
	lh = &Lighthouse{}
	lh.SvcsMap = setupServicesMap()
	//lh.Zones = []string{"cluster.local."}
	lh.Fall = fall.F{Zones: []string{"cluster.local."}}
	// we use RcodeBadCookie as indicator for next plugin called.
	lh.Next = test.NextHandler(dns.RcodeBadCookie, errors.New("dummy plugin"))

	rec := dnstest.NewRecorder(&test.ResponseWriter{})
	ctx = context.TODO()

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
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
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
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("WriteMsg fails", func() {
		BeforeEach(func() {
			tc = test.Case{
				// service does not exist, expect error.
				Qname: "mysvc.default.svc.cluster.local.", Qtype: dns.TypeA,
				Rcode: dns.RcodeServerFailure,
			}
			lh.Zones = []string{"cluster.local."}
			rec = dnstest.NewRecorder(&FailingResponseWriter{errorMsg: "write failed"})
		})
		It("Should return error RcodeServerFailure", func() {
			m := tc.Msg()
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

}
