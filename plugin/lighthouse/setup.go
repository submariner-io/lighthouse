package lighthouse

import (
	"flag"
	"fmt"

	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/submariner-io/lighthouse/pkg/gateway"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
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

	l, err := lighthouseParse(c)
	if err != nil {
		return plugin.Error("lighthouse", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		l.Next = next
		return l
	})

	return nil
}

func lighthouseParse(c *caddy.Controller) (*Lighthouse, error) {
	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("error building kubeconfig: %v", err)
	}

	siMap := serviceimport.NewMap()
	siController := serviceimport.NewController(siMap)

	err = siController.Start(cfg)
	if err != nil {
		return nil, fmt.Errorf("error starting the ServiceImport controller: %v", err)
	}

	gwController := gateway.NewController()
	err = gwController.Start(cfg)
	if err != nil {
		return nil, fmt.Errorf("error starting the Gateway controller: %v", err)
	}

	c.OnShutdown(func() error {
		siController.Stop()
		gwController.Stop()
		return nil
	})

	lh := &Lighthouse{serviceImports: siMap, clusterStatus: gwController}

	// Changed `for` to `if` to satisfy golint:
	//	 SA4004: the surrounding loop is unconditionally terminated (staticcheck)
	if c.Next() {
		lh.Zones = c.RemainingArgs()
		if len(lh.Zones) == 0 {
			lh.Zones = make([]string, len(c.ServerBlockKeys))
			copy(lh.Zones, c.ServerBlockKeys)
		}

		for i, str := range lh.Zones {
			lh.Zones[i] = plugin.Host(str).Normalize()
		}

		for c.NextBlock() {
			switch c.Val() {
			case "fallthrough":
				lh.Fall.SetZonesFromArgs(c.RemainingArgs())
			default:
				if c.Val() != "}" {
					return nil, c.Errf("unknown property '%s'", c.Val())
				}
			}
		}
	}

	return lh, nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
