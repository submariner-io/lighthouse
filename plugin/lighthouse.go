package lighthouse

import (
	"context"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

// Define log to be a logger with the plugin name in it. This way we can just use log.Info and
// friends to log.
var log = clog.NewWithPlugin("lighthouse")

type Lighthouse struct {
	Next plugin.Handler
}

// ServeDNS implements the plugin.Handler interface.
func (lh Lighthouse) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	// Debug log that we've have seen the query. This will only be shown when the debug plugin is loaded.
	log.Debugf("Received query from '%s'", state.Name())


	// Use a non-writer so we don't write NXDOMAIN to client
	nw := nonwriter.New(w)

	// Call next plugin (if any).
	return plugin.NextOrFailure(lh.Name(), lh.Next, ctx, nw, r)
}

// Name implements the Handler interface.
func (lh Lighthouse) Name() string {
	return "lighthouse"
}

