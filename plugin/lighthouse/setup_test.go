package lighthouse

import (
	"github.com/caddyserver/caddy"
	"k8s.io/client-go/rest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[setup] Test basic bring up", func() {
	When("start is called", testStart)
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

func testStart() {
	BuildConfigFromFlags = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
		if kubeconfigPath == "" {
			return &rest.Config{}, nil
		}
		return nil, nil
	}
}
