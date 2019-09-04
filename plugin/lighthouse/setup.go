package lighthouse

import (
	"os"
	"strings"

	"github.com/caddyserver/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

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
	lh := Lighthouse{}
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
		lh.SvcsMap = setupServicesMap()
		return &lh, nil
	}
	return &lh, nil
}

func setupServicesMap() ServicesMap {
	svcs := make(ServicesMap)
	svcArray := os.Getenv("LIGHTHOUSE_SVCS")
	if svcArray == "" {
		return svcs
	}
	strArray := strings.Split(svcArray, ",")
	for _, str := range strArray {
		svc := strings.Split(str, "=")
		if len(svc) == 2 {
			svcs[svc[0]] = svc[1]
		}
	}
	return svcs
}
