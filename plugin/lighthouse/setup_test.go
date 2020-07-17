package lighthouse

import (
	"context"
	"errors"

	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/gateway"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
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
	BeforeEach(func() {
		gateway.NewClientset = func(c *rest.Config) (dynamic.Interface, error) {
			return fakeClient.NewSimpleDynamicClient(runtime.NewScheme()), nil
		}
	})

	AfterEach(func() {
		gateway.NewClientset = nil
	})

	Context("Parsing correct configurations", testCorrectConfig)
	Context("Parsing incorrect configurations", testIncorrectConfig)
	Context("Plugin registration", testPluginRegistration)
})

func testCorrectConfig() {
	var (
		lh     *Lighthouse
		config string
	)

	BeforeEach(func() {
		buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
			return &rest.Config{}, nil
		}

		config = "lighthouse"
	})

	JustBeforeEach(func() {
		var err error
		lh, err = lighthouseParse(caddy.NewTestController("dns", config))
		Expect(err).To(Succeed())
	})

	When("no optional arguments are specified", func() {
		It("should succeed with empty zones and fallthrough fields", func() {
			Expect(lh.Fall).To(Equal(fall.F{}))
			Expect(lh.Zones).To(BeEmpty())
		})
	})

	When("lighthouse zone and fallthrough zone arguments are specified", func() {
		BeforeEach(func() {
			config = `lighthouse cluster2.local cluster3.local {
			    fallthrough cluster2.local
            }`
		})

		It("should succeed with the zones and fallthrough fields populated correctly", func() {
			Expect(lh.Fall).To(Equal(fall.F{Zones: []string{"cluster2.local."}}))
			Expect(lh.Zones).To(Equal([]string{"cluster2.local.", "cluster3.local."}))
		})
	})

	When("fallthrough argument with no zones is specified", func() {
		BeforeEach(func() {
			config = `lighthouse {
			    fallthrough
            }`
		})

		It("should succeed with the root fallthrough zones", func() {
			Expect(lh.Fall).Should(Equal(fall.Root))
			Expect(lh.Zones).Should(BeEmpty())
		})
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
	var (
		setupErr error
		config   string
	)

	JustBeforeEach(func() {
		setupErr = setupLighthouse(caddy.NewTestController("dns", config))
	})

	When("an unexpected argument is specified", func() {
		BeforeEach(func() {
			config = `lighthouse {
                dummy
		    } noplugin`

			buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
				return &rest.Config{}, nil
			}
		})

		It("should return an appropriate plugin error", func() {
			verifyPluginError(setupErr, "dummy")
		})
	})

	When("building the kubeconfig fails", func() {
		BeforeEach(func() {
			config = "lighthouse"

			buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
				return nil, errors.New("mock")
			}
		})

		It("should return an appropriate plugin error", func() {
			verifyPluginError(setupErr, "mock")
		})
	})
}

func testPluginRegistration() {
	When("plugin setup succeeds", func() {
		It("should properly register the Lighthouse plugin with the DNS server", func() {
			buildKubeConfigFunc = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
				return &rest.Config{}, nil
			}

			controller := caddy.NewTestController("dns", "lighthouse")
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
}

func verifyPluginError(err error, str string) {
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(HavePrefix("plugin/lighthouse"))
	Expect(err.Error()).To(ContainSubstring(str))
}
