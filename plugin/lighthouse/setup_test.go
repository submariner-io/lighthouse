package lighthouse

import (
	"context"
	"errors"

	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
)

type fakeHandler struct {
}

func (f *fakeHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return dns.RcodeSuccess, nil
}

func (f *fakeHandler) Name() string {
	return "fake"
}

var _ = Describe("Plugin setup", func() {
	var (
		controller *caddy.Controller
	)

	BeforeEach(func() {
		buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
			return &rest.Config{}, nil
		}

		controller = caddy.NewTestController("dns", "lighthouse")
	})

	When("the proper arguments are provided", func() {
		It("should succeed and the Lighthouse plugin should be registered", func() {
			err := setupLighthouse(controller)
			Expect(err).To(Succeed())

			plugins := dnsserver.GetConfig(controller).Plugin
			Expect(plugins).To(HaveLen(1))

			fakeHandler := &fakeHandler{}
			handler := plugins[0](fakeHandler)
			lh, ok := handler.(*Lighthouse)
			Expect(ok).To(BeTrue(), "Unexpected Handler type %T", handler)
			Expect(lh.Next).To(BeIdenticalTo(fakeHandler))
		})
	})

	When("an unexpected argument is provided", func() {
		It("should return a plugin error", func() {
			controller := caddy.NewTestController("dns", "lighthouse unexpectedArg")
			err := setupLighthouse(controller)
			verifyPluginError(err)
		})
	})

	When("building the kubeconfig fails", func() {
		It("should return a plugin error", func() {
			buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
				return nil, errors.New("mock")
			}

			err := setupLighthouse(controller)
			verifyPluginError(err)
		})
	})
})

func verifyPluginError(err error) {
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(HavePrefix("plugin/lighthouse"))
}
