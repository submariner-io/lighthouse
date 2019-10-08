package lighthouse

import (
	"flag"
	"fmt"

	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	masterURL  string
	kubeconfig string
)

// Hook for unit tests
var buildKubeConfigFunc = clientcmd.BuildConfigFromFlags

// init registers this plugin within the Caddy plugin framework. It uses "example" as the
// name, and couples it to the Action "setup".
func init() {
	caddy.RegisterPlugin("lighthouse", caddy.Plugin{
		ServerType: "dns",
		Action:     setupLighthouse,
	})
}

// setup is the function that gets called when the config parser see the token "lighthouse". Setup is responsible
// for parsing any extra options the this plugin may have. The first token this function sees is "lighthouse".
func setupLighthouse(c *caddy.Controller) error {
	log.Debug("In setupLighthouse")

	c.Next() // Ignore "lighthouse" and give us the next token.
	if c.NextArg() {
		// If there was another token, return an error, because we currently don't have any configuration.
		// Any errors returned from this setup function should be wrapped with plugin.Error, so we
		// can present a slightly nicer error message to the user.
		return plugin.Error("lighthouse", c.ArgErr())
	}

	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		return plugin.Error("lighthouse", fmt.Errorf("error building kubeconfig: %v", err))
	}

	mcsMap := new(multiClusterServiceMap)
	dnsController := newController(mcsMap)

	err = dnsController.start(cfg)
	if err != nil {
		return plugin.Error("lighthouse", fmt.Errorf("error starting the controller: %v", err))
	}

	c.OnShutdown(func() error {
		dnsController.stop()
		return nil
	})

	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &Lighthouse{Next: next, multiClusterServices: mcsMap}
	})

	// All OK, return a nil error.
	return nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
