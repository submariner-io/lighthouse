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
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newServiceImportController(spec *AgentSpecification, syncerMetricNames AgentConfig, syncerConfig broker.SyncerConfig,
	brokerClient dynamic.Interface, brokerNamespace string, serviceExportClient *ServiceExportClient,
) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		localClient:             syncerConfig.LocalClient,
		restMapper:              syncerConfig.RestMapper,
		clusterID:               spec.ClusterID,
		localNamespace:          spec.Namespace,
		converter:               converter{scheme: syncerConfig.Scheme},
		serviceImportAggregator: newServiceImportAggregator(brokerClient, brokerNamespace, spec.ClusterID, syncerConfig.Scheme),
		serviceExportClient:     serviceExportClient,
	}

	syncCounter := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: syncerMetricNames.ServiceImportCounterName,
			Help: "Count of imported services",
		},
		[]string{
			syncer.DirectionLabel,
			syncer.OperationLabel,
			syncer.SyncerNameLabel,
		},
	)
	prometheus.MustRegister(syncCounter)

	var err error

	controller.localSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Local ServiceImport",
		SourceClient:    syncerConfig.LocalClient,
		SourceNamespace: controller.localNamespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      syncerConfig.RestMapper,
		Federator:       controller,
		ResourceType:    &mcsv1a1.ServiceImport{},
		Transform:       controller.onLocalServiceImport,
		Scheme:          syncerConfig.Scheme,
		SyncCounter:     syncCounter,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating local ServiceImport syncer")
	}

	controller.serviceImportMigrator = &ServiceImportMigrator{
		clusterID:                          spec.ClusterID,
		localNamespace:                     spec.Namespace,
		brokerClient:                       brokerClient.Resource(serviceImportGVR).Namespace(brokerNamespace),
		listLocalServiceImports:            controller.localSyncer.ListResources,
		converter:                          converter{scheme: syncerConfig.Scheme},
		deletedLocalServiceImportsOnBroker: sets.New[string](),
	}

	controller.remoteSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:             "Remote ServiceImport",
		SourceClient:     brokerClient,
		SourceNamespace:  brokerNamespace,
		RestMapper:       syncerConfig.RestMapper,
		Federator:        federate.NewCreateOrUpdateFederator(syncerConfig.LocalClient, syncerConfig.RestMapper, corev1.NamespaceAll, ""),
		ResourceType:     &mcsv1a1.ServiceImport{},
		Transform:        controller.onRemoteServiceImport,
		OnSuccessfulSync: controller.serviceImportMigrator.onSuccessfulSyncFromBroker,
		Scheme:           syncerConfig.Scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating ServiceImport watcher")
	}

	if spec.GlobalnetEnabled {
		controller.globalIngressIPCache, err = newGlobalIngressIPCache(watcher.Config{
			RestMapper: syncerConfig.RestMapper,
			Client:     syncerConfig.LocalClient,
			Scheme:     syncerConfig.Scheme,
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

		logger.Info("ServiceImport Controller stopped")
	}()

	if err := c.localSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting local ServiceImport syncer")
	}

	if err := c.remoteSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting remote ServiceImport syncer")
	}

	c.reconcileLocalAggregatedServiceImports()
	c.reconcileRemoteAggregatedServiceImports()

	return nil
}

func (c *ServiceImportController) reconcileRemoteAggregatedServiceImports() {
	c.localSyncer.Reconcile(func() []runtime.Object {
		siList, err := c.remoteSyncer.ListResources()
		if err != nil {
			logger.Error(err, "Error listing serviceImports")
			return nil
		}

		retList := make([]runtime.Object, 0, len(siList))
		for i := range siList {
			si := c.converter.toServiceImport(siList[i])

			serviceName, ok := si.Annotations[mcsv1a1.LabelServiceName]
			if !ok {
				// This is not an aggregated ServiceImport.
				continue
			}

			if slices.IndexOf(si.Status.Clusters, c.clusterID, clusterStatusKey) < 0 {
				continue
			}

			si.Name = serviceName + "-" + si.Annotations[constants.LabelSourceNamespace] + "-" + c.clusterID
			si.Namespace = c.localNamespace
			si.Labels = map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: si.Annotations[constants.LabelSourceNamespace],
			}

			retList = append(retList, si)
		}

		return retList
	})
}

func (c *ServiceImportController) reconcileLocalAggregatedServiceImports() {
	c.remoteSyncer.Reconcile(func() []runtime.Object {
		siList, err := c.localClient.Resource(serviceImportGVR).Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			logger.Error(err, "Error listing ServiceImports")
			return nil
		}

		retList := make([]runtime.Object, 0, len(siList.Items))
		for i := range siList.Items {
			si := c.converter.toServiceImport(&siList.Items[i])

			if serviceImportSourceName(si) != "" {
				// This is not an aggregated ServiceImport.
				continue
			}

			si.Annotations = map[string]string{
				mcsv1a1.LabelServiceName:       si.Name,
				constants.LabelSourceNamespace: si.Namespace,
			}

			si.Name = fmt.Sprintf("%s-%s", si.Name, si.Namespace)
			si.Namespace = c.serviceImportAggregator.brokerNamespace

			retList = append(retList, si)
		}

		return retList
	})
}

func (c *ServiceImportController) startEndpointsController(serviceImport *mcsv1a1.ServiceImport) error {
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	if obj, found := c.endpointControllers.LoadAndDelete(key); found {
		logger.V(log.DEBUG).Infof("Stopping previous endpoints controller for %q", key)
		obj.(*EndpointController).stop()
	}

	endpointController, err := startEndpointController(c.localClient, c.restMapper, c.converter.scheme,
		serviceImport, c.clusterID, c.globalIngressIPCache)
	if err != nil {
		return errors.Wrapf(err, "failed to start endpoints controller for %q", key)
	}

	c.endpointControllers.Store(key, endpointController)

	return nil
}

func (c *ServiceImportController) stopEndpointsController(key string) (bool, error) {
	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.stop()

		found, err := endpointController.cleanup()
		if err == nil {
			c.endpointControllers.Delete(key)
		}

		return found, err
	}

	return false, nil
}

func (c *ServiceImportController) onLocalServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	serviceName := serviceImportSourceName(serviceImport)

	sourceCluster := sourceClusterName(serviceImport)
	if sourceCluster != c.clusterID {
		return nil, false
	}

	logger.V(log.DEBUG).Infof("Local ServiceImport %q %sd", key, op)

	if op == syncer.Delete {
		c.serviceExportClient.updateStatusConditions(serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			newServiceExportCondition(constants.ServiceExportSynced,
				corev1.ConditionFalse, "NoServiceImport", "ServiceImport was deleted"))

		return obj, false
	} else if op == syncer.Create {
		c.serviceExportClient.updateStatusConditions(serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			newServiceExportCondition(constants.ServiceExportSynced,
				corev1.ConditionFalse, "AwaitingExport", fmt.Sprintf("ServiceImport %sd - awaiting aggregation on the broker", op)))
	}

	return obj, false
}

func (c *ServiceImportController) Distribute(obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localServiceImport)

	logger.V(log.DEBUG).Infof("Distribute for local ServiceImport %q", key)

	serviceName := serviceImportSourceName(localServiceImport)
	serviceNamespace := localServiceImport.Labels[constants.LabelSourceNamespace]

	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace),
			Annotations: map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: serviceNamespace,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  localServiceImport.Spec.Type,
			Ports: []mcsv1a1.ServicePort{},
		},
	}

	conflict := false

	// Here we just create the aggregated ServiceImport on the broker. We don't merge the local service info until we've
	// successfully synced our local EndpointSlice to the broker. This is mainly done b/c the aggregated port information
	// is determined from the constituent clusters' EndpointSlices, thus each cluster must have a consistent view of all
	// the EndpointSlices in order for the aggregated port information to be eventually consistent.

	result, err := util.CreateOrUpdate(context.Background(), resource.ForDynamic(c.serviceImportAggregator.brokerServiceImportClient()),
		c.converter.toUnstructured(aggregate),
		func(obj runtime.Object) (runtime.Object, error) {
			existing := c.converter.toServiceImport(obj)

			if localServiceImport.Spec.Type != existing.Spec.Type {
				conflict = true
				conflictCondition := newServiceExportCondition(
					mcsv1a1.ServiceExportConflict, corev1.ConditionTrue, typeConflictReason,
					fmt.Sprintf("The service type %q does not match the type (%q) of the existing service export",
						localServiceImport.Spec.Type, existing.Spec.Type))

				c.serviceExportClient.updateStatusConditions(serviceName, serviceNamespace, conflictCondition,
					newServiceExportCondition(constants.ServiceExportSynced,
						corev1.ConditionFalse, exportFailedReason, "Unable to export due to an irresolvable conflict"))
			} else {
				c.serviceExportClient.removeStatusCondition(serviceName, serviceNamespace, mcsv1a1.ServiceExportConflict, typeConflictReason)
			}

			return obj, nil
		})
	if err == nil && !conflict {
		err = c.startEndpointsController(localServiceImport)
	}

	if err != nil {
		c.serviceExportClient.updateStatusConditions(serviceName, serviceNamespace,
			newServiceExportCondition(constants.ServiceExportSynced,
				corev1.ConditionFalse, exportFailedReason, fmt.Sprintf("Unable to export: %v", err)))
	}

	if result == util.OperationResultCreated {
		logger.V(log.DEBUG).Infof("Created aggregated ServiceImport %q", aggregate.Name)
	}

	return err
}

func (c *ServiceImportController) Delete(obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localServiceImport)

	logger.V(log.DEBUG).Infof("Delete for local ServiceImport %q", key)

	// For consistency, we let the EndpointSlice controller handle removing the local service info from the aggregated
	// ServiceImport on the broker after we delete the local EndpointSlice here. However, if the Endpoints controller
	// was never started or if there are no local EndpointSlices, which can happen during reconciliation on startup or
	// during clean up on uninstall, then we handle removal here.

	found, err := c.stopEndpointsController(key)
	if err != nil {
		return err
	}

	if !found {
		err = c.serviceImportAggregator.updateOnDelete(serviceImportSourceName(localServiceImport),
			localServiceImport.Labels[constants.LabelSourceNamespace])
	}

	if err != nil {
		return err
	}

	return c.serviceImportMigrator.onLocalServiceImportDeleted(localServiceImport)
}

func (c *ServiceImportController) onRemoteServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)

	serviceName, ok := serviceImport.Annotations[mcsv1a1.LabelServiceName]
	if ok {
		// This is an aggregated ServiceImport - sync it to the local service namespace.
		serviceImport.Name = serviceName
		serviceImport.Namespace = serviceImport.Annotations[constants.LabelSourceNamespace]

		delete(serviceImport.Annotations, mcsv1a1.LabelServiceName)
		delete(serviceImport.Annotations, constants.LabelSourceNamespace)

		return serviceImport, false
	}

	return c.serviceImportMigrator.onRemoteServiceImport(serviceImport)
}

func (c *ServiceImportController) localServiceImportLister(transform func(si *mcsv1a1.ServiceImport) runtime.Object) []runtime.Object {
	siList, err := c.localSyncer.ListResources()
	if err != nil {
		logger.Error(err, "Error listing serviceImports")
		return nil
	}

	retList := make([]runtime.Object, 0, len(siList))

	for _, obj := range siList {
		si := obj.(*mcsv1a1.ServiceImport)

		clusterID := sourceClusterName(si)
		if clusterID != c.clusterID {
			continue
		}

		retList = append(retList, transform(si))
	}

	return retList
}
