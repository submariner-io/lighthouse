package lighthouse

import (
	"context"
	"os"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[setup] Test basic DNS Handler", func() {
	Describe("function serveDNS", testDnsServer)
})

func testDnsServer() {
	var (
		lh  *Lighthouse
		ctx context.Context
		tc  test.Case
	)
	tests := []test.Case{
		{
			// service is present.
			Qname: "mysvc.default.svc.cluster.local.", Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("mysvc.default.svc.cluster.local.    0    IN    A    100.96.156.175"),
			},
		},
		{
			// service does not exist, expect error.
			Qname: "mysvc2.default.svc.cluster.local.", Qtype: dns.TypeA,
			Rcode: dns.RcodeServerFailure,
		},
		{
			// service does not exist, expect error.
			Qname: "mysvc2.default.svc.cluster.local.", Qtype: dns.TypeAAAA,
			Rcode: dns.RcodeServerFailure,
		},
	}

	os.Setenv("LIGHTHOUSE_SVCS", "mysvc=100.96.156.175,dummy=100.96.156.175,none=100.96.156.100")
	lh = &Lighthouse{}
	lh.SvcsMap = setupServicesMap()

	ctx = context.TODO()

	When("DNS query for typeA record", func() {
		BeforeEach(func() {
			tc = tests[0]
		})
		It("Should return A record for existent query", func() {
			m := tc.Msg()
			rec := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).NotTo(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
			respErr := test.SortAndCheck(rec.Msg, tc)
			Expect(respErr).NotTo(HaveOccurred())
		})
	})

	When("DNS query for typeA record", func() {
		BeforeEach(func() {
			tc = tests[1]
		})
		It("Should return an error for non existent query", func() {
			m := tc.Msg()
			rec := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})

	When("DNS query for typeAAAA record", func() {
		BeforeEach(func() {
			tc = tests[2]
		})
		It("Should return an error", func() {
			m := tc.Msg()
			rec := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := lh.ServeDNS(ctx, rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})
}
