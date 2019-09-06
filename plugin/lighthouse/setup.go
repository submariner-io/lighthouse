package lighthouse

import (
	"flag"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	mcservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"

	"github.com/caddyserver/caddy"
)

type remoteServiceMap map[string]*mcservice.MultiClusterService

var (
	masterURL  string
	kubeconfig string
)
var BuildConfigFromFlags = clientcmd.BuildConfigFromFlags

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
	klog.Infof("setupLighthouse")
	c.Next() // Ignore "lighthouse" and give us the next token.
	if c.NextArg() {
		// If there was another token, return an error, because we currently don't have any configuration.
		// Any errors returned from this setup function should be wrapped with plugin.Error, so we
		// can present a slightly nicer error message to the user.
		return plugin.Error("lighthouse", c.ArgErr())
	}
	rsMap := make(remoteServiceMap)
	lpController := New(rsMap)
	cfg, err := BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("error building kubeconfig: %s", err.Error())
	}
	err = lpController.Start(cfg)
	if err != nil {
		klog.Fatalf("error starting the controller: %s", err.Error())
	}
	c.OnShutdown(func() error {
		lpController.Stop()
		return nil
	})
	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &Lighthouse{Next: next, RemoteServiceMap: rsMap}
	})

	// All OK, return a nil error.
	return nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
