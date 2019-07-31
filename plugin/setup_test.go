package lighthouse

import (
	"github.com/caddyserver/caddy"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[setup] Test basic bring up", func() {

	It("Should come up without errors", func() {
		c := caddy.NewTestController("dns", `lighthouse`)
		err := setupLighthouse(c)
		Expect(err).NotTo(HaveOccurred())
	})

	It("Should give error on setup", func() {
		c := caddy.NewTestController("dns", `lighthouse more`)
		err := setupLighthouse(c)
		Expect(err).To(HaveOccurred())
	})

})
