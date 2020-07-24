package lighthouse

import (
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[parse] Test parse DNS request", func() {
	Context("When request is valid", testParseValid)
	Context("When request is invalid", testParseInvalid)
})

func testParseValid() {
	type parseTest struct {
		query    string
		expected string // output from r.String()
	}

	When("SRV request", func() {
		It("Should give no error", func() {
			tc := parseTest{"_http._tcp.webs.mynamespace.svc.inter.webs.tests.", "http.tcp..webs.mynamespace.svc"}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("Wildcard request", func() {
		It("Should give no error", func() {
			tc := parseTest{"*.any.*.any.svc.inter.webs.tests.", "*.any..*.any.svc"}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("A request of endpoint", func() {
		It("Should give no error", func() {
			tc := parseTest{"1-2-3-4.webs.mynamespace.svc.inter.webs.tests.", "*.*.1-2-3-4.webs.mynamespace.svc"}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("Bare zone", func() {
		It("Should give no error", func() {
			tc := parseTest{"inter.webs.tests.", "....."}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("Bare svc type", func() {
		It("Should give no error", func() {
			tc := parseTest{"svc.inter.webs.tests.", "....."}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("Bare pod type", func() {
		It("Should give no error", func() {
			tc := parseTest{"pod.inter.webs.tests.", "....."}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
}

func testParseInvalid() {
	When("request not for SVC or POD", func() {
		It("Should give error", func() {
			m := new(dns.Msg)
			m.SetQuestion("webs.mynamespace.pood.inter.webs.test.", dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			_, e := parseRequest(state)
			Expect(e).To(HaveOccurred())
		})
	})
	When("request too long", func() {
		It("Should give error", func() {
			m := new(dns.Msg)
			m.SetQuestion("too.long.for.what.I.am.trying.to.pod.inter.webs.tests.", dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			_, e := parseRequest(state)
			Expect(e).To(HaveOccurred())
		})
	})
}

const zone = "inter.webs.tests."
