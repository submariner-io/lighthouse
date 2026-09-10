/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"sync"
	"time"

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/ipam"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

const (
	ServiceExportReasonNoServiceImport                mcsv1b1.ServiceExportConditionReason = "NoServiceImport"
	ServiceExportReasonRetrievalFailed                mcsv1b1.ServiceExportConditionReason = "ServiceRetrievalFailed"
	ServiceExportReasonClusterSetIPEnablementConflict mcsv1b1.ServiceExportConditionReason = "ClusterSetIPEnablementConflict"
	ServiceExportReasonUnsupportedIPFamily            mcsv1b1.ServiceExportConditionReason = "UnsupportedIPFamily"
	ServiceExportReasonGlobalIPUnavailable            mcsv1b1.ServiceExportConditionReason = "ServiceGlobalIPUnavailable"
	ServiceExportReasonRestrictedNamespace            mcsv1b1.ServiceExportConditionReason = "RestrictedNamespace"
)

type EndpointSliceListerFn func(selector k8slabels.Selector) []runtime.Object

type AggregatedServiceImportGetterFn func(serviceName string, serviceNamespace string) *mcsv1b1.ServiceImport

type converter struct {
	scheme *runtime.Scheme
}

type Controller struct {
	clusterID                   string
	globalnetEnabled            bool
	namespace                   string
	serviceExportClient         *ServiceExportClient
	serviceExportSyncer         syncer.Interface
	endpointSliceController     *EndpointSliceController
	serviceSyncer               syncer.Interface
	serviceImportController     *ServiceImportController
	localServiceImportFederator federate.Federator
	namespaceInformer           cache.SharedInformer
	clusterLocalReflector       *ClusterLocalReflector
	namespaceValidator          *NamespaceValidator
}

type AgentSpecification struct {
	ClusterID           string
	Namespace           string
	ClustersetIPCidr    string `split_words:"true"`
	ClustersetIPEnabled bool   `split_words:"true"`
	GlobalnetEnabled    bool   `split_words:"true"`
	Uninstall           bool
	HaltOnCertError     bool `split_words:"true"`
	// ReflectClusterLocal, when true, makes the agent reflect each imported service
	// into a native cluster.local Service + EndpointSlice (<svc>-<sourceCluster>) so
	// discovery works on clusters whose DNS cannot host the clusterset.local stanza
	// (e.g. EKS Auto Mode's immutable node-local CoreDNS). Opt-in; default false.
	ReflectClusterLocal bool `split_words:"true"`
	Debug               bool
}

// The ServiceImportController encapsulates two resource syncers; one that watches for local cluster ServiceImports
// from the submariner namespace and creates/updates the aggregated ServiceImport on the broker; the other that syncs
// aggregated ServiceImports from the broker to the local service namespace. It also creates a ServiceEndpointSliceController.
type ServiceImportController struct {
	localClient                dynamic.Interface
	brokerClient               dynamic.Interface
	brokerNamespace            string
	restMapper                 meta.RESTMapper
	serviceExportClient        *ServiceExportClient
	localSyncer                syncer.Interface
	remoteSyncer               syncer.Interface
	localFederator             federate.Federator
	supportedIPFamilies        []corev1.IPFamily
	endpointControllers        sync.Map
	clusterID                  string
	localNamespace             string
	converter                  converter
	globalIngressIPCache       *globalIngressIPCache
	localLHEndpointSliceLister EndpointSliceListerFn
	clustersetIPPool           *ipam.IPPool
	clustersetIPEnabled        bool
	namespaceValidator         *NamespaceValidator
}

// ServiceEndpointSliceController watches for the EndpointSlices that backs a Service and have a ServiceImport.
// It creates LH EndpointSlices corresponding to service EndpointSlices that are distributed to other clusters.
type ServiceEndpointSliceController struct {
	clusterID                string
	serviceName              string
	serviceNamespace         string
	serviceImportSpec        *mcsv1b1.ServiceImportSpec
	publishNotReadyAddresses string
	stopCh                   chan struct{}
	stopOnce                 sync.Once
	localClient              dynamic.ResourceInterface
	ingressIPClient          dynamic.NamespaceableResourceInterface
	globalIngressIPCache     *globalIngressIPCache
	epsSyncer                syncer.Interface
	awaitStoppedTimeout      time.Duration
}

// EndpointSliceController encapsulates a syncer that syncs EndpointSlices to and from that broker.
type EndpointSliceController struct {
	clusterID           string
	syncer              *broker.Syncer
	serviceExportClient *ServiceExportClient
	serviceSyncer       syncer.Interface
	localClient         dynamic.Interface
	namespaceValidator  *NamespaceValidator
}

type ServiceExportClient struct {
	dynamic.NamespaceableResourceInterface
	converter
	localSyncer syncer.Interface
}

type globalIngressIPEntry struct {
	obj           *unstructured.Unstructured
	onAddOrUpdate func()
}

type globalIngressIPTransformFn func(obj *unstructured.Unstructured) (any, bool)

type globalIngressIPMap struct {
	sync.Mutex
	entries map[string]*globalIngressIPEntry
}

type globalIngressIPCache struct {
	byService   globalIngressIPMap
	byPod       globalIngressIPMap
	byEndpoints globalIngressIPMap
	watcher     watcher.Interface
}
