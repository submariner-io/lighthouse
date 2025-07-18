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
	"net"
	"strconv"

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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newServiceImportController(spec *AgentSpecification, agentConfig AgentConfig, syncerConfig broker.SyncerConfig,
	brokerClient dynamic.Interface, brokerNamespace string, serviceExportClient *ServiceExportClient,
	localLHEndpointSliceLister EndpointSliceListerFn,
) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		localClient:                syncerConfig.LocalClient,
		restMapper:                 syncerConfig.RestMapper,
		clusterID:                  spec.ClusterID,
		localNamespace:             spec.Namespace,
		converter:                  converter{scheme: syncerConfig.Scheme},
		serviceImportAggregator:    newServiceImportAggregator(brokerClient, brokerNamespace, spec.ClusterID, syncerConfig.Scheme),
		serviceExportClient:        serviceExportClient,
		localLHEndpointSliceLister: localLHEndpointSliceLister,
		clustersetIPPool:           agentConfig.IPPool,
		clustersetIPEnabled:        spec.ClustersetIPEnabled,
	}

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
		SyncCounterOpts: &prometheus.GaugeOpts{
			Name: agentConfig.ServiceExportCounterName,
			Help: "Count of exported services",
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating local ServiceImport syncer")
	}

	controller.remoteSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:              "Remote ServiceImport",
		SourceClient:      brokerClient,
		SourceNamespace:   brokerNamespace,
		RestMapper:        syncerConfig.RestMapper,
		Federator:         federate.NewCreateOrUpdateFederator(syncerConfig.LocalClient, syncerConfig.RestMapper, corev1.NamespaceAll, ""),
		ResourceType:      &mcsv1a1.ServiceImport{},
		Transform:         controller.onRemoteServiceImport,
		OnSuccessfulSync:  controller.onSuccessfulSyncFromBroker,
		Scheme:            syncerConfig.Scheme,
		NamespaceInformer: syncerConfig.NamespaceInformer,
		SyncCounterOpts: &prometheus.GaugeOpts{
			Name: agentConfig.ServiceImportCounterName,
			Help: "Count of imported services",
		},
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

		c.endpointControllers.Range(func(_, value interface{}) bool {
			err := value.(*ServiceEndpointSliceController).stop(context.TODO())
			if err != nil {
				logger.Warningf("Error stopping service EndpointSlice controller: %s", err)
			}

			return true
		})

		logger.Info("ServiceImport Controller stopped")
	}()

	if err := c.reserveAggregatedServiceImportIPs(); err != nil {
		return err
	}

	if err := c.localSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting local ServiceImport syncer")
	}

	if err := c.remoteSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting remote ServiceImport syncer")
	}

	c.reconcileLocalAggregatedServiceImports()
	c.reconcileRemoteAggregatedServiceImports()
	c.reconcileLocalServiceImportsOnBroker()

	return nil
}

func (c *ServiceImportController) isIPInClustersetCIDR(si *mcsv1a1.ServiceImport) bool {
	if c.clustersetIPPool == nil || len(si.Spec.IPs) == 0 {
		return false
	}

	ip := net.ParseIP(si.Spec.IPs[0])
	_, cidr, _ := net.ParseCIDR(c.clustersetIPPool.GetCIDR())

	return ip != nil && cidr.Contains(ip)
}

func (c *ServiceImportController) reserveAggregatedServiceImportIPs() error {
	client := c.localClient.Resource(serviceImportGVR).Namespace(corev1.NamespaceAll)

	list, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing the local ServiceImports")
	}

	for i := range list.Items {
		si := c.converter.toServiceImport(&list.Items[i])

		if serviceImportSourceName(si) != "" || !c.isIPInClustersetCIDR(si) {
			continue
		}

		err = c.clustersetIPPool.Reserve(si.Spec.IPs[0])
		if err != nil {
			logger.Errorf(err, "Unable to reserve clusterset IP %q in CIDR %q for ServiceImport %s",
				si.Spec.IPs[0], c.clustersetIPPool.GetCIDR(), resource.ToJSON(si))
		}
	}

	return nil
}

func (c *ServiceImportController) reconcileRemoteAggregatedServiceImports() {
	c.localSyncer.Reconcile(func() []runtime.Object {
		siList := c.remoteSyncer.ListResources()
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

func (c *ServiceImportController) reconcileLocalServiceImportsOnBroker() {
	c.localSyncer.Reconcile(func() []runtime.Object {
		siList := c.remoteSyncer.ListResources()
		retList := make([]runtime.Object, 0, len(siList))

		for i := range siList {
			si := c.converter.toServiceImport(siList[i])

			if si.Annotations[mcsv1a1.LabelServiceName] != "" ||
				si.Labels[mcsv1a1.LabelSourceCluster] != c.clusterID {
				// This is an aggregated ServiceImport or another cluster's local ServiceImport.
				continue
			}

			si.Namespace = c.localNamespace
			si.Name = si.Labels[mcsv1a1.LabelServiceName] + "-" + si.Labels[constants.LabelSourceNamespace] + "-" + c.clusterID

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

			if serviceImportSourceName(si) != "" || si.Annotations[mcsv1a1.LabelServiceName] != "" {
				// This is not a local aggregated ServiceImport.
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

func (c *ServiceImportController) startEndpointsController(ctx context.Context, serviceImport *mcsv1a1.ServiceImport) error {
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	if obj, found := c.endpointControllers.Load(key); found {
		logger.V(log.DEBUG).Infof("Stopping previous EndpointSlice controller for %q", key)

		err := obj.(*ServiceEndpointSliceController).stop(ctx)
		if err != nil {
			return errors.Wrapf(err, "failed to stop previous EndpointSlice controller for %q", key)
		}

		c.endpointControllers.Delete(key)
	}

	endpointController, err := startEndpointSliceController(c.localClient, c.restMapper, c.converter.scheme,
		serviceImport, c.clusterID, c.globalIngressIPCache, c.localLHEndpointSliceLister)
	if err != nil {
		return errors.Wrapf(err, "failed to start EndpointSlice controller for %q", key)
	}

	c.endpointControllers.Store(key, endpointController)

	return nil
}

func (c *ServiceImportController) stopEndpointsController(ctx context.Context, key string) (bool, error) {
	if obj, found := c.endpointControllers.Load(key); found {
		var err error

		endpointController := obj.(*ServiceEndpointSliceController)
		err = endpointController.stop(ctx)

		if err == nil {
			found, err = endpointController.cleanup(ctx)
			if err == nil {
				c.endpointControllers.Delete(key)
			}
		}

		return found, err
	}

	return false, nil
}

func (c *ServiceImportController) onLocalServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)
	ctx := context.TODO()

	serviceName := serviceImportSourceName(serviceImport)

	if serviceImport.Labels[mcsv1a1.LabelSourceCluster] != c.clusterID {
		return nil, false
	}

	logger.V(log.DEBUG).Infof("Local ServiceImport %q %sd", key, op)

	if op == syncer.Delete {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			newServiceExportCondition(constants.ServiceExportReady,
				metav1.ConditionFalse, NoServiceImportReason, "ServiceImport was deleted"))

		return obj, false
	} else if op == syncer.Create {
		c.serviceExportClient.tryUpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			false, newServiceExportCondition(constants.ServiceExportReady,
				metav1.ConditionFalse, AwaitingExportReason, fmt.Sprintf("ServiceImport %sd - awaiting aggregation on the broker", op)))
	}

	return obj, false
}

func (c *ServiceImportController) Distribute(ctx context.Context, obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localServiceImport)

	logger.V(log.DEBUG).Infof("Distribute for local ServiceImport %q", key)

	serviceName := serviceImportSourceName(localServiceImport)
	serviceNamespace := localServiceImport.Labels[constants.LabelSourceNamespace]

	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: brokerAggregatedServiceImportName(serviceName, serviceNamespace),
			Annotations: map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: serviceNamespace,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  localServiceImport.Spec.Type,
			Ports: []mcsv1a1.ServicePort{},
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: c.clusterID,
				},
			},
		},
	}

	typeConflict := false
	clusterSetIP := ""

	useClusterSetIP := c.determineUseClusterSetIP(localServiceImport)

	// Create the aggregated ServiceImport on the broker or update the existing instance with our local service info.
	result, newAggregate, err := util.CreateOrUpdateWithOptions(ctx, util.CreateOrUpdateOptions[*unstructured.Unstructured]{
		Client: resource.ForDynamic(c.serviceImportAggregator.brokerServiceImportClient()),
		Obj:    c.converter.toUnstructured(aggregate),
		MutateOnUpdate: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			existing := c.converter.toServiceImport(obj)

			if localServiceImport.Spec.Type != existing.Spec.Type {
				typeConflict = true
				c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
					newServiceExportCondition(constants.ServiceExportReady,
						metav1.ConditionFalse, ExportFailedReason, "Unable to export due to an irresolvable conflict"))
			} else {
				if existing.Annotations == nil {
					existing.Annotations = map[string]string{}
				}

				if _, found := existing.Annotations[constants.UseClustersetIP]; !found {
					// This will happen on migration from pre-clusterset IP version
					existing.Annotations[constants.UseClustersetIP] = strconv.FormatBool(false)
				}

				var added bool

				existing.Status.Clusters, added = slices.AppendIfNotPresent(existing.Status.Clusters,
					mcsv1a1.ClusterStatus{Cluster: c.clusterID}, clusterStatusKey)

				if added {
					logger.V(log.DEBUG).Infof("Added cluster name %q to aggregated ServiceImport %q. New status: %#v",
						c.clusterID, existing.Name, existing.Status.Clusters)
				}
			}

			return c.converter.toUnstructured(existing), nil
		},
		MutateOnCreate: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			si := c.converter.toServiceImport(obj)

			if si.Spec.Type != mcsv1a1.ClusterSetIP {
				return obj, nil
			}

			var err error

			if useClusterSetIP {
				clusterSetIP, err = c.allocateClusterSetIPIfNeeded(clusterSetIP)

				si.Spec.IPs = []string{clusterSetIP}
				si.Annotations[constants.ClustersetIPAllocatedBy] = c.clusterID
			}

			si.Annotations[constants.UseClustersetIP] = strconv.FormatBool(useClusterSetIP)

			return c.converter.toUnstructured(si), err
		},
	})
	if err == nil && !typeConflict {
		err = c.startEndpointsController(ctx, localServiceImport)
	}

	if err != nil {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
			newServiceExportCondition(constants.ServiceExportReady,
				metav1.ConditionFalse, ExportFailedReason, fmt.Sprintf("Unable to export: %v", err)))

		if clusterSetIP != "" {
			_ = c.clustersetIPPool.Release(clusterSetIP)
		}
	}

	if result == util.OperationResultCreated {
		logger.V(log.DEBUG).Infof("Created aggregated ServiceImport %s", resource.ToJSON(newAggregate))
	}

	if err == nil {
		err = c.createLocalServiceImportOnBroker(ctx, localServiceImport)
	}

	return err
}

func brokerAggregatedServiceImportName(serviceName, serviceNamespace string) string {
	return fmt.Sprintf("%s-%s", serviceName, serviceNamespace)
}

func (c *ServiceImportController) Delete(ctx context.Context, obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localServiceImport)

	logger.V(log.DEBUG).Infof("Delete for local ServiceImport %q", key)

	_, err := c.stopEndpointsController(ctx, key)
	if err != nil {
		return err
	}

	err = c.serviceImportAggregator.updateOnDelete(ctx, serviceImportSourceName(localServiceImport),
		localServiceImport.Labels[constants.LabelSourceNamespace])
	if err != nil {
		return err
	}

	list, err := c.serviceImportAggregator.brokerServiceImportClient().List(ctx, metav1.ListOptions{
		LabelSelector: k8slabels.Set(map[string]string{
			mcsv1a1.LabelServiceName:       localServiceImport.Labels[mcsv1a1.LabelServiceName],
			constants.LabelSourceNamespace: localServiceImport.Labels[constants.LabelSourceNamespace],
			mcsv1a1.LabelSourceCluster:     localServiceImport.Labels[mcsv1a1.LabelSourceCluster],
		}).String(),
	})
	if err != nil {
		return errors.Wrap(err, "error listing ServiceImport resources for delete")
	}

	if len(list.Items) == 0 {
		return nil
	}

	return errors.Wrapf(c.serviceImportAggregator.brokerServiceImportClient().Delete(ctx, list.Items[0].GetName(), metav1.DeleteOptions{}),
		"error deleting ServiceImport %q on the broker", list.Items[0].GetName())
}

func (c *ServiceImportController) createLocalServiceImportOnBroker(ctx context.Context, localServiceImport *mcsv1a1.ServiceImport) error {
	useClusterSetIP := c.determineUseClusterSetIP(localServiceImport)

	localServiceImport.ObjectMeta = metav1.ObjectMeta{
		GenerateName: serviceImportSourceName(localServiceImport) + "-",
		Labels:       localServiceImport.Labels,
		Annotations:  localServiceImport.Annotations,
	}

	localServiceImport.Annotations[constants.UseClustersetIP] = strconv.FormatBool(useClusterSetIP)
	localServiceImport.Status = mcsv1a1.ServiceImportStatus{}

	result, si, err := util.CreateOrUpdateWithOptions(ctx, util.CreateOrUpdateOptions[*unstructured.Unstructured]{
		Client: resource.ForDynamic(c.serviceImportAggregator.brokerServiceImportClient()),
		Obj:    c.converter.toUnstructured(localServiceImport),
		IdentifyingLabels: map[string]string{
			mcsv1a1.LabelServiceName:       localServiceImport.Labels[mcsv1a1.LabelServiceName],
			constants.LabelSourceNamespace: localServiceImport.Labels[constants.LabelSourceNamespace],
			mcsv1a1.LabelSourceCluster:     localServiceImport.Labels[mcsv1a1.LabelSourceCluster],
		},
		MutateOnUpdate: func(existing *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			return util.CopyImmutableMetadata(existing, c.converter.toUnstructured(localServiceImport)), nil
		},
	})

	if result == util.OperationResultCreated {
		logger.V(log.DEBUG).Infof("Created local ServiceImport %q on the broker", si.GetName())
	}

	return err //nolint:wrapcheck // No need to wrap
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

	ctx := context.TODO()
	serviceName = serviceImport.Labels[mcsv1a1.LabelServiceName]
	serviceNamespace := serviceImport.Labels[constants.LabelSourceNamespace]

	localServiceExport := c.serviceExportClient.getLocalInstance(serviceName, serviceNamespace)
	if localServiceExport == nil {
		return nil, false
	}

	aggregatedObj, err := c.serviceImportAggregator.brokerServiceImportClient().Get(ctx,
		brokerAggregatedServiceImportName(serviceName, serviceNamespace), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false
	}

	if err != nil {
		logger.Errorf(err, "Error retrieving aggregated ServiceImport \"%s%s\"", serviceNamespace, serviceName)
		return nil, true
	}

	aggregatedServiceImport := c.converter.toServiceImport(aggregatedObj)

	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	logger.V(log.DEBUG).Infof("ServiceImport %q from cluster %q %sd on broker",
		key, serviceImport.Labels[mcsv1a1.LabelSourceCluster], op)

	precedentServiceImport := c.checkForConflicts(ctx, aggregatedServiceImport)
	if precedentServiceImport == nil {
		return nil, false
	}

	isPrecedentCluster := precedentServiceImport.Labels[mcsv1a1.LabelSourceCluster] == c.clusterID
	if isPrecedentCluster {
		err = c.serviceImportAggregator.update(ctx, serviceName, serviceNamespace,
			func(aggregated *mcsv1a1.ServiceImport) error {
				aggregated.Spec.SessionAffinity = precedentServiceImport.Spec.SessionAffinity
				aggregated.Spec.SessionAffinityConfig = precedentServiceImport.Spec.SessionAffinityConfig
				aggregated.Spec.Ports = precedentServiceImport.Spec.Ports

				return nil
			})
		if err != nil {
			logger.Errorf(err, "Error updating aggregated ServiceImport \"%s%s\"", serviceNamespace, serviceName)
		}
	}

	return nil, err != nil
}

func (c *ServiceImportController) onSuccessfulSyncFromBroker(synced runtime.Object, op syncer.Operation) bool {
	aggregatedServiceImport := synced.(*mcsv1a1.ServiceImport)

	if op == syncer.Delete {
		if c.isIPInClustersetCIDR(aggregatedServiceImport) {
			_ = c.clustersetIPPool.Release(aggregatedServiceImport.Spec.IPs[0])
		}
	}

	return false
}

func (c *ServiceImportController) determineUseClusterSetIP(localServiceImport *mcsv1a1.ServiceImport) bool {
	var useClusterSetIP bool

	useClusterSetIPStr, found := localServiceImport.Annotations[constants.UseClustersetIP]
	if found {
		useClusterSetIP = useClusterSetIPStr == strconv.FormatBool(true)
	} else {
		useClusterSetIP = c.clustersetIPEnabled
	}

	return useClusterSetIP && c.clustersetIPPool != nil
}

func (c *ServiceImportController) allocateClusterSetIPIfNeeded(existingIP string) (string, error) {
	if existingIP == "" {
		allocatedIPs, err := c.clustersetIPPool.Allocate(1)
		if err != nil {
			return "", errors.Wrap(err, "unable to allocate clusterset IP from the pool")
		}

		existingIP = allocatedIPs[0]
	}

	return existingIP, nil
}

func (c *ServiceImportController) localServiceImportLister(transform func(si *mcsv1a1.ServiceImport) runtime.Object) []runtime.Object {
	siList := c.localSyncer.ListResources()

	retList := make([]runtime.Object, 0, len(siList))

	for _, obj := range siList {
		si := obj.(*mcsv1a1.ServiceImport)

		if si.Labels[mcsv1a1.LabelSourceCluster] != c.clusterID {
			continue
		}

		retList = append(retList, transform(si))
	}

	return retList
}

func serviceImportSourceName(serviceImport *mcsv1a1.ServiceImport) string {
	return serviceImport.Labels[mcsv1a1.LabelServiceName]
}
