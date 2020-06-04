package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Controller struct {
	clusterID     string
	restConfig    *rest.Config
	kubeClientSet kubernetes.Interface
	svcSyncer     *broker.Syncer
}

type AgentSpecification struct {
	ClusterID string
	Namespace string
}
