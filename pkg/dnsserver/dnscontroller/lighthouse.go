package dnscontroller

import (
	"github.com/submariner-io/lighthouse/pkg/multiclusterservice"
)

type Lighthouse struct {
	MultiClusterServices *multiclusterservice.Map
}
