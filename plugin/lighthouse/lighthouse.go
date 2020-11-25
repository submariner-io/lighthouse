package lighthouse

import (
	"errors"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
)

const (
	Svc        = "svc"
	Pod        = "pod"
	defaultTtl = uint32(5)
)

var (
	errInvalidRequest = errors.New("invalid query name")
)

// Define log to be a logger with the plugin name in it. This way we can just use log.Info and
// friends to log.
var log = clog.NewWithPlugin("lighthouse")

type Lighthouse struct {
	Next            plugin.Handler
	Fall            fall.F
	Zones           []string
	ttl             uint32
	serviceImports  *serviceimport.Map
	endpointSlices  *endpointslice.Map
	clusterStatus   ClusterStatus
	endpointsStatus EndpointsStatus
	localServices   LocalServices
}

type ClusterStatus interface {
	IsConnected(clusterID string) bool

	LocalClusterID() string
}

type LocalServices interface {
	GetIP(name, namespace string) (string, bool)
}

type EndpointsStatus interface {
	IsHealthy(name, namespace, clusterId string) bool
}

var _ plugin.Handler = &Lighthouse{}
