/*
© 2020 Red Hat, Inc. and others

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

	"k8s.io/client-go/kubernetes"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

type Controller struct {
	clusterID                 string
	globalnetEnabled          bool
	namespace                 string
	kubeClientSet             kubernetes.Interface
	serviceExportClient       dynamic.NamespaceableResourceInterface
	serviceExportSyncer       syncer.Interface
	serviceImportSyncer       *broker.Syncer
	endpointSliceSyncer       *broker.Syncer
	serviceSyncer             syncer.Interface
	serviceImportController   *ServiceImportController
	lhServiceExportController *LHServiceExportController
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
	localClient           dynamic.Interface
}
