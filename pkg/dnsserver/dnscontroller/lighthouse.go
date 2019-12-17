package dnscontroller

import (
	"errors"

	clog "github.com/coredns/coredns/plugin/pkg/log"
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
	MultiClusterServices *MultiClusterServiceMap
}

type handler struct{}
