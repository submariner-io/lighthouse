package lighthouse

import (
	"errors"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/submariner-io/lighthouse/pkg/multiclusterservice"
)

const (
	Svc = "svc"
	Pod = "pod"
)

var (
	errInvalidRequest = errors.New("invalid query name")
)

// Define log to be a logger with the plugin name in it. This way we can just use log.Info and
// friends to log.
var log = clog.NewWithPlugin("lighthouse")

type Lighthouse struct {
	Next                 plugin.Handler
	Fall                 fall.F
	Zones                []string
	multiClusterServices *multiclusterservice.Map
}

var _ plugin.Handler = &Lighthouse{}
