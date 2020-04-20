package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"k8s.io/client-go/rest"
)

type Controller struct {
	clusterID         string
	restConfig        *rest.Config
	excludeNamespaces map[string]bool
	svcSyncer         *broker.Syncer
}

type AgentSpecification struct {
	ClusterID string
	Namespace string
	ExcludeNS []string
}
