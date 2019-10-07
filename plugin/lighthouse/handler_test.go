package lighthouse

import (
	"context"

	"github.com/coredns/coredns/plugin/pkg/dnstest"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[setup] Test basic DNS Handler", func() {
	Describe("function serveDNS", testDnsServer)
})

func testDnsServer() {
	const (
		service1   = "service1"
		namespace1 = "namespace1"
		serviceIP  = "100.96.156.175"
	)

	var (
		lh *Lighthouse
		tc test.Case
	)
	tests := []test.Case{
		{
			// service is present.
			Qname: "service1.namespace1.svc.cluster.local.", Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A(service1 + "." + namespace1 + ".svc.cluster.local.    0    IN    A    " + serviceIP),
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

	mcs1 := &lighthousev1.MultiClusterService{
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
	}

	lh = &Lighthouse{}
	lh.multiClusterServices = new(multiClusterServiceMap)
	lh.multiClusterServices.put(mcs1)

	When("DNS query for typeA record", func() {
		BeforeEach(func() {
			tc = tests[0]
		})
		It("Should return A record for existent query", func() {
			m := tc.Msg()
			rec := dnstest.NewRecorder(&test.ResponseWriter{})
			code, err := lh.ServeDNS(context.TODO(), rec, m)
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
			code, err := lh.ServeDNS(context.TODO(), rec, m)
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
			code, err := lh.ServeDNS(context.TODO(), rec, m)
			Expect(err).To(HaveOccurred())
			Expect(code).Should(Equal(tc.Rcode))
		})
	})
}
