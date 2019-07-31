package lighthouse

import (
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
	c.Next() // Ignore "lighthouse" and give us the next token.
	if c.NextArg() {
		// If there was another token, return an error, because we currently don't have any configuration.
		// Any errors returned from this setup function should be wrapped with plugin.Error, so we
		// can present a slightly nicer error message to the user.
		return plugin.Error("lighthouse", c.ArgErr())
	}

	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return Lighthouse{Next: next}
	})

	// All OK, return a nil error.
	return nil
}
