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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/watcher"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newServiceImportController(spec *AgentSpecification, serviceSyncer syncer.Interface, restMapper meta.RESTMapper,
	localClient dynamic.Interface, scheme *runtime.Scheme) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		serviceSyncer: serviceSyncer,
		localClient:   localClient,
		restMapper:    restMapper,
		clusterID:     spec.ClusterID,
		scheme:        scheme,
	}

	var err error

	controller.serviceImportSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceImport watcher",
		SourceClient:    localClient,
		SourceNamespace: spec.Namespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       federate.NewNoopFederator(),
		ResourceType:    &mcsv1a1.ServiceImport{},
		Transform:       controller.serviceImportToEndpointController,
		Scheme:          scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating ServiceImport watcher")
	}

	if spec.GlobalnetEnabled {
		controller.globalIngressIPCache, err = newGlobalIngressIPCache(watcher.Config{
			RestMapper: restMapper,
			Client:     localClient,
			Scheme:     scheme,
		})
	}

	return controller, err
}

func (c *ServiceImportController) start(stopCh <-chan struct{}) error {
	if c.globalIngressIPCache != nil {
		if err := c.globalIngressIPCache.start(stopCh); err != nil {
			return err
		}
	}

	go func() {
		<-stopCh

		c.endpointControllers.Range(func(key, value interface{}) bool {
			value.(*EndpointController).stop()
			return true
		})

		klog.Infof("ServiceImport Controller stopped")
	}()

	if err := c.serviceImportSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ServiceImport watcher")
	}

	return nil
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(serviceImport *mcsv1a1.ServiceImport, key string) bool {
	if _, found := c.endpointControllers.Load(key); found {
		klog.V(log.DEBUG).Infof("The endpoint controller is already running for %q", key)
		return false
	}

	if serviceImport.GetLabels()[lhconstants.LighthouseLabelSourceCluster] != c.clusterID {
		return false
	}

	annotations := serviceImport.ObjectMeta.Annotations
	serviceNameSpace := annotations[lhconstants.OriginNamespace]
	serviceName := annotations[lhconstants.OriginName]

	obj, found, err := c.serviceSyncer.GetResource(serviceName, serviceNameSpace)
	if err != nil {
		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, err)

		return true
	}

	if !found {
		return false
	}

	service := obj.(*corev1.Service)
	if service.Spec.Selector == nil {
		klog.Errorf("The service %s/%s without a Selector is not supported", serviceNameSpace, serviceName)
		return false
	}

	endpointController, err := startEndpointController(c.localClient, c.restMapper, c.scheme,
		serviceImport, serviceNameSpace, serviceName, c.clusterID, c.globalIngressIPCache)
	if err != nil {
		klog.Errorf(err.Error())
		return true
	}

	c.endpointControllers.Store(key, endpointController)

	return false
}

func (c *ServiceImportController) serviceImportDeleted(serviceImport *mcsv1a1.ServiceImport, key string) {
	if serviceImport.GetLabels()[lhconstants.LighthouseLabelSourceCluster] != c.clusterID {
		return
	}

	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.stop()
		c.endpointControllers.Delete(key)
	}
}

func (c *ServiceImportController) serviceImportToEndpointController(obj runtime.Object, numRequeues int,
	op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	klog.V(log.DEBUG).Infof("ServiceImport %q %sd", key, op)

	if op == syncer.Create || op == syncer.Update {
		return nil, c.serviceImportCreatedOrUpdated(serviceImport, key)
	}

	c.serviceImportDeleted(serviceImport, key)

	return nil, false
}
