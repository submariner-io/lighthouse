package controller

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
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

// The ServiceImportController listens for ServiceImport resources created in the target namespace
// and creates an EndpointController in response. The EndpointController will use the app label as filter
// to listen only for the endpoints event related to ServiceImport created
type ServiceImportController struct {
	kubeClientSet           kubernetes.Interface
	lighthouseClient        lighthouseClientset.Interface
	serviceInformer         cache.SharedIndexInformer
	queue                   workqueue.RateLimitingInterface
	endpointControllers     sync.Map
	serviceImportDeletedMap sync.Map
	clusterID               string
	namespace               string
}

// Each EndpointController listens for the endpoints that backs a service and have a ServiceImport
// It will create an endpoint slice corresponding to an endpoint object and set the owner references
// to ServiceImport. The app label from the endpoint will be added to endpoint slice as well.
type EndpointController struct {
	kubeClientSet                kubernetes.Interface
	endpointInformer             cache.Controller
	store                        cache.Store
	endPointqueue                workqueue.RateLimitingInterface
	serviceImportUID             types.UID
	clusterID                    string
	serviceImportName            string
	serviceImportSourceNameSpace string
	endpointDeletedMap           sync.Map
	stopCh                       chan struct{}
}

type EndpointSliceController struct {
	kubeClientSet           kubernetes.Interface
	endpointSliceInformer   cache.SharedIndexInformer
	endPointSlicequeue      workqueue.RateLimitingInterface
	clusterID               string
	endpointSliceDeletedMap sync.Map
	namespace               string
	stopCh                  chan struct{}
}

const (
	originName           = "origin-name"
	originNamespace      = "origin-namespace"
	labelSourceName      = "lighthouse.submariner.io/sourceName"
	labelSourceNamespace = "lighthouse.submariner.io/sourceNamespace"
	labelSourceCluster   = "lighthouse.submariner.io/sourceCluster"
	labelManagedBy       = "endpointslice.kubernetes.io/managed-by"
	labelValueManagedBy  = "lighthouse-agent.submariner.io"
)
