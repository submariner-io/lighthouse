package lighthouse

import (
	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/plugin/pkg/fall"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[setup] Test lighthouse bring up", func() {
	Context("When configuration is correct", testCorrectConfig)
	Context("When configuration is incorrect", testIncorrectConfig)
})

func testCorrectConfig() {
	It("Should come up without errors", func() {
		c := caddy.NewTestController("dns", `lighthouse`)
		lh, err := lighthouseParse(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(lh.Fall).Should(Equal(fall.F{Zones: nil}))
		Expect(lh.Zones).Should(BeEmpty())
	})

	It("Should populate fields correctly", func() {
		config := `lighthouse cluster2.local cluster3.local {
			fallthrough cluster2.local
		}`
		zones := []string{"cluster2.local.", "cluster3.local."}
		fallZones := fall.F{Zones: []string{"cluster2.local."}}

		c := caddy.NewTestController("dns", config)
		lh, err := lighthouseParse(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(lh.Fall).Should(Equal(fallZones))
		Expect(lh.Zones).Should(Equal(zones))
	})

	It("Should handle empty zones", func() {
		config := `lighthouse {
			fallthrough
		}`
		c := caddy.NewTestController("dns", config)
		lh, err := lighthouseParse(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(lh.Fall).Should(Equal(fall.Root))
		Expect(lh.Zones).Should(BeEmpty())
	})

	It("Should handle missing optional fields", func() {
		config := `lighthouse`
		c := caddy.NewTestController("dns", config)
		lh, err := lighthouseParse(c)
		Expect(err).NotTo(HaveOccurred())
		setupErr := setupLighthouse(c) // For coverage
		Expect(setupErr).NotTo(HaveOccurred())
		Expect(lh.Fall).Should(Equal(fall.F{}))
		Expect(lh.Zones).Should(BeEmpty())
	})
}

func testIncorrectConfig() {
	It("Should come up with errors for unsupported keywords", func() {
		config := `lighthouse {
            dummy
		}
		noplugin`
		c := caddy.NewTestController("dns", config)
		lh, err := lighthouseParse(c)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("dummy"))
		setupErr := setupLighthouse(c) // For coverage
		Expect(setupErr).To(HaveOccurred())
		Expect(lh).Should(BeNil())

	})
}
