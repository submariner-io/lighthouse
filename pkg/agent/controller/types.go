package controller

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Controller struct {
	clusterID                  string
	globalnetEnabled           bool
	namespace                  string
	kubeClientSet              kubernetes.Interface
	serviceExportClient        dynamic.NamespaceableResourceInterface
	serviceExportSyncer        *broker.Syncer
	endpointSliceSyncer        *broker.Syncer
	serviceSyncer              syncer.Interface
	serviceImportController    *ServiceImportController
	serviceLHExportController  *LHServiceExportController
	serviceMCSExportController *MCSServiceExportController
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
	serviceSyncer       syncer.Interface
	localClient         dynamic.Interface
	restMapper          meta.RESTMapper
	serviceImportSyncer syncer.Interface
	endpointControllers sync.Map
	clusterID           string
	scheme              *runtime.Scheme
}

// Each EndpointController listens for the endpoints that backs a service and have a ServiceImport
// It will create an endpoint slice corresponding to an endpoint object and set the owner references
// to ServiceImport. The app label from the endpoint will be added to endpoint slice as well.
type EndpointController struct {
	serviceImportUID             types.UID
	clusterID                    string
	serviceImportName            string
	serviceName                  string
	serviceImportSourceNameSpace string
	stopCh                       chan struct{}
}

type LHServiceExportController struct {
	lhServiceExportSyncer syncer.Interface
}

type MCSServiceExportController struct {
	mcsServiceExportSyncer syncer.Interface
	lighthouseClient       lighthouseClientset.Interface
}
