/*
© 2020 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package lighthouse

import (
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const bareResult = "...."

var _ = Describe("[parse] Test parse DNS request", func() {
	Context("When request is valid", testParseValid)
	Context("When request is invalid", testParseInvalid)
})

func testParseValid() {
	type parseTest struct {
		query    string
		expected string // output from r.String()
	}

	When("SVC request", func() {
		It("Should give no error", func() {
			tc := parseTest{"webs.mynamespace.svc.inter.webs.tests.", "..webs.mynamespace.svc"}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("An SVC request of hostname", func() {
		It("Should give no error", func() {
			tc := parseTest{"host1.cluster1.webs.mynamespace.svc.inter.webs.tests.", "host1.cluster1.webs.mynamespace.svc"}
			m := new(dns.Msg)
			m.SetQuestion(tc.query, dns.TypeA)
			state := request.Request{Zone: zone, Req: m}
			r, e := parseRequest(state)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.String()).Should(Equal(tc.expected))
		})
	})
	When("An SVC request of cluster", func() {
		It("Should give no error", func() {
			tc := parseTest{"cluster1.webs.mynamespace.svc.inter.webs.tests.", ".cluster1.webs.mynamespace.svc"}
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
			tc := parseTest{"*.any.*.any.svc.inter.webs.tests.", "*.any.*.any.svc"}
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
			tc := parseTest{"inter.webs.tests.", bareResult}
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
			tc := parseTest{"svc.inter.webs.tests.", bareResult}
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
			tc := parseTest{"pod.inter.webs.tests.", bareResult}
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
