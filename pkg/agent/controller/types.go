package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"k8s.io/client-go/kubernetes"
)

type Controller struct {
	clusterID           string
	globalnetEnabled    bool
	namespace           string
	kubeClientSet       kubernetes.Interface
	lighthouseClient    lighthouseClientset.Interface
	serviceExportSyncer *broker.Syncer
	serviceSyncer       syncer.Interface
	endpointSyncer      syncer.Interface
}

type AgentSpecification struct {
	ClusterID        string
	Namespace        string
	GlobalnetEnabled bool `split_words:"true"`
}
