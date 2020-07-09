package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Controller struct {
	clusterID        string
	globalnetEnabled bool
	restConfig       *rest.Config
	kubeClientSet    kubernetes.Interface
	lighthouseClient lighthouseClientset.Interface
	svcSyncer        *broker.Syncer
}

type AgentSpecification struct {
	ClusterID        string
	Namespace        string
	GlobalnetEnabled bool `split_words:"true"`
}
