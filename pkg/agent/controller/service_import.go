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
	"math"
	"net"
	"reflect"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/set"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const timestampAnnotationPrefix = "timestamp.submariner.io/"

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

	controller.serviceImportMigrator = &ServiceImportMigrator{
		clusterID:                          spec.ClusterID,
		localNamespace:                     spec.Namespace,
		brokerClient:                       brokerClient.Resource(serviceImportGVR).Namespace(brokerNamespace),
		listLocalServiceImports:            controller.localSyncer.ListResources,
		converter:                          converter{scheme: syncerConfig.Scheme},
		deletedLocalServiceImportsOnBroker: set.New[string](),
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
			value.(*ServiceEndpointSliceController).stop()
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

func (c *ServiceImportController) startEndpointsController(serviceImport *mcsv1a1.ServiceImport) error {
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)

	if obj, found := c.endpointControllers.LoadAndDelete(key); found {
		logger.V(log.DEBUG).Infof("Stopping previous endpoints controller for %q", key)
		obj.(*ServiceEndpointSliceController).stop()
	}

	endpointController, err := startEndpointSliceController(c.localClient, c.restMapper, c.converter.scheme,
		serviceImport, c.clusterID, c.globalIngressIPCache, c.localLHEndpointSliceLister)
	if err != nil {
		return errors.Wrapf(err, "failed to start endpoints controller for %q", key)
	}

	c.endpointControllers.Store(key, endpointController)

	return nil
}

func (c *ServiceImportController) stopEndpointsController(ctx context.Context, key string) (bool, error) {
	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*ServiceEndpointSliceController)
		endpointController.stop()

		found, err := endpointController.cleanup(ctx)
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
	ctx := context.TODO()

	serviceName := serviceImportSourceName(serviceImport)

	sourceCluster := sourceClusterName(serviceImport)
	if sourceCluster != c.clusterID {
		return nil, false
	}

	logger.V(log.DEBUG).Infof("Local ServiceImport %q %sd", key, op)

	if op == syncer.Delete {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			newServiceExportCondition(constants.ServiceExportReady,
				corev1.ConditionFalse, "NoServiceImport", "ServiceImport was deleted"))

		return obj, false
	} else if op == syncer.Create {
		c.serviceExportClient.tryUpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			false, newServiceExportCondition(constants.ServiceExportReady,
				corev1.ConditionFalse, "AwaitingExport", fmt.Sprintf("ServiceImport %sd - awaiting aggregation on the broker", op)))
	}

	return obj, false
}

func (c *ServiceImportController) Distribute(ctx context.Context, obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localServiceImport)

	logger.V(log.DEBUG).Infof("Distribute for local ServiceImport %q", key)

	serviceName := serviceImportSourceName(localServiceImport)
	serviceNamespace := localServiceImport.Labels[constants.LabelSourceNamespace]

	localTimestamp := strconv.FormatInt(int64(math.MaxInt64-1), 10)

	// As per the MCS spec, a conflict will be resolved by assigning precedence based on each ServiceExport's
	// creationTimestamp, from oldest to newest. We don't have access to other cluster's ServiceExports so
	// instead add our ServiceExport's creationTimestamp as an annotation on the aggregated ServiceImport.
	localServiceExport := c.serviceExportClient.getLocalInstance(serviceName, serviceNamespace)
	if localServiceExport != nil {
		localTimestamp = strconv.FormatInt(localServiceExport.CreationTimestamp.UTC().UnixNano(), 10)
	}

	timestampAnnotationKey := makeTimestampAnnotationKey(c.clusterID)

	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: brokerAggregatedServiceImportName(serviceName, serviceNamespace),
			Annotations: map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: serviceNamespace,
				timestampAnnotationKey:         localTimestamp,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:                  localServiceImport.Spec.Type,
			Ports:                 []mcsv1a1.ServicePort{},
			SessionAffinity:       localServiceImport.Spec.SessionAffinity,
			SessionAffinityConfig: localServiceImport.Spec.SessionAffinityConfig,
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

	// Here we create the aggregated ServiceImport on the broker or update the existing instance with our local service
	// info, but we don't add/merge our local service ports until we've successfully synced our local EndpointSlice to
	// the broker. This is mainly done b/c the aggregated port information is determined from the constituent clusters'
	// EndpointSlices, thus each cluster must have a consistent view of all the EndpointSlices in order for the
	// aggregated port information to be eventually consistent.

	result, newAggregate, err := util.CreateOrUpdateWithOptions(ctx, util.CreateOrUpdateOptions[*unstructured.Unstructured]{
		Client: resource.ForDynamic(c.serviceImportAggregator.brokerServiceImportClient()),
		Obj:    c.converter.toUnstructured(aggregate),
		MutateOnUpdate: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			existing := c.converter.toServiceImport(obj)

			if localServiceImport.Spec.Type != existing.Spec.Type {
				typeConflict = true
				conflictCondition := newServiceExportCondition(
					mcsv1a1.ServiceExportConflict, corev1.ConditionTrue, TypeConflictReason,
					fmt.Sprintf("The local service type (%q) does not match the type (%q) of the existing exported service",
						localServiceImport.Spec.Type, existing.Spec.Type))

				c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace, conflictCondition,
					newServiceExportCondition(constants.ServiceExportReady,
						corev1.ConditionFalse, ExportFailedReason, "Unable to export due to an irresolvable conflict"))
			} else {
				if c.serviceExportClient.hasCondition(serviceName, serviceNamespace, mcsv1a1.ServiceExportConflict, TypeConflictReason) {
					c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace, newServiceExportCondition(
						mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, TypeConflictReason, ""))
				}

				if existing.Annotations == nil {
					existing.Annotations = map[string]string{}
				}

				existing.Annotations[timestampAnnotationKey] = localTimestamp

				if _, found := existing.Annotations[constants.UseClustersetIP]; !found {
					// This will happen on migration from pre-clusterset IP version
					existing.Annotations[constants.UseClustersetIP] = strconv.FormatBool(false)
				}

				// Update the appropriate aggregated ServiceImport fields if we're the oldest exporting cluster
				_ = c.updateAggregatedServiceImport(existing, localServiceImport)

				c.checkConflicts(ctx, existing, localServiceImport, &useClusterSetIP)

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
		err = c.startEndpointsController(localServiceImport)
	}

	if err != nil {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
			newServiceExportCondition(constants.ServiceExportReady,
				corev1.ConditionFalse, ExportFailedReason, fmt.Sprintf("Unable to export: %v", err)))

		if clusterSetIP != "" {
			_ = c.clustersetIPPool.Release(clusterSetIP)
		}
	}

	if result == util.OperationResultCreated {
		logger.V(log.DEBUG).Infof("Created aggregated ServiceImport %s", resource.ToJSON(newAggregate))
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

	// For consistency, we let the EndpointSlice controller handle removing the local service info from the aggregated
	// ServiceImport on the broker after we delete the local EndpointSlice here. However, if the Endpoints controller
	// was never started or if there are no local EndpointSlices, which can happen during reconciliation on startup or
	// during clean up on uninstall, then we handle removal here.

	found, err := c.stopEndpointsController(ctx, key)
	if err != nil {
		return err
	}

	if !found {
		err = c.serviceImportAggregator.updateOnDelete(ctx, serviceImportSourceName(localServiceImport),
			localServiceImport.Labels[constants.LabelSourceNamespace])
	}

	if err != nil {
		return err
	}

	return c.serviceImportMigrator.onLocalServiceImportDeleted(ctx, localServiceImport)
}

func (c *ServiceImportController) onRemoteServiceImport(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {
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

func (c *ServiceImportController) onSuccessfulSyncFromBroker(synced runtime.Object, op syncer.Operation) bool {
	ctx := context.TODO()

	retry := c.serviceImportMigrator.onSuccessfulSyncFromBroker(synced, op)

	aggregatedServiceImport := synced.(*mcsv1a1.ServiceImport)

	if op == syncer.Delete {
		if c.isIPInClustersetCIDR(aggregatedServiceImport) {
			_ = c.clustersetIPPool.Release(aggregatedServiceImport.Spec.IPs[0])
		}

		return retry
	}

	// Check for conflicts with the local ServiceImport

	siList := c.localSyncer.ListResourcesBySelector(k8slabels.SelectorFromSet(map[string]string{
		mcsv1a1.LabelServiceName:        aggregatedServiceImport.Name,
		constants.LabelSourceNamespace:  aggregatedServiceImport.Namespace,
		constants.MCSLabelSourceCluster: c.clusterID,
	}))

	if len(siList) == 0 {
		// Service not exported locally.
		return retry
	}

	localServiceImport := siList[0].(*mcsv1a1.ServiceImport)

	// This handles the case where the previously oldest exporting cluster has unexported its service. If we're now
	// the oldest exporting cluster, then update the appropriate aggregated ServiceImport fields to match those of
	// our service's.
	if c.updateAggregatedServiceImport(aggregatedServiceImport, localServiceImport) {
		err := c.serviceImportAggregator.update(ctx, aggregatedServiceImport.Name, aggregatedServiceImport.Namespace,
			func(aggregated *mcsv1a1.ServiceImport) error {
				aggregated.Spec.SessionAffinity = localServiceImport.Spec.SessionAffinity
				aggregated.Spec.SessionAffinityConfig = localServiceImport.Spec.SessionAffinityConfig

				return nil
			})
		if err != nil {
			logger.Errorf(err, "error updating aggregated ServiceImport on broker sync")

			return true
		}
	}

	c.checkConflicts(ctx, aggregatedServiceImport, localServiceImport, nil)

	return retry
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

func (c *ServiceImportController) checkConflicts(ctx context.Context, aggregated, local *mcsv1a1.ServiceImport, useClusterSetIP *bool) {
	var conditions []mcsv1a1.ServiceExportCondition

	serviceName := local.Labels[mcsv1a1.LabelServiceName]
	serviceNamespace := local.Labels[constants.LabelSourceNamespace]

	precedentCluster := findClusterWithOldestTimestamp(aggregated.Annotations)

	if local.Spec.SessionAffinity != aggregated.Spec.SessionAffinity {
		conditions = append(conditions, newServiceExportCondition(mcsv1a1.ServiceExportConflict, corev1.ConditionTrue,
			SessionAffinityConflictReason,
			fmt.Sprintf("The local service SessionAffinity %q conflicts with other constituent clusters. "+
				"Using SessionAffinity %q from the oldest exported service in cluster %q.",
				local.Spec.SessionAffinity, aggregated.Spec.SessionAffinity, precedentCluster)))
	} else if c.serviceExportClient.hasCondition(serviceName, serviceNamespace, mcsv1a1.ServiceExportConflict,
		SessionAffinityConflictReason) {
		conditions = append(conditions, newServiceExportCondition(
			mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, SessionAffinityConflictReason, ""))
	}

	if !reflect.DeepEqual(local.Spec.SessionAffinityConfig, aggregated.Spec.SessionAffinityConfig) {
		conditions = append(conditions, newServiceExportCondition(mcsv1a1.ServiceExportConflict, corev1.ConditionTrue,
			SessionAffinityConfigConflictReason,
			fmt.Sprintf("The local service SessionAffinityConfig %q conflicts with other constituent clusters. "+
				"Using SessionAffinityConfig %q from the oldest exported service in cluster %q.",
				toSessionAffinityConfigString(local.Spec.SessionAffinityConfig),
				toSessionAffinityConfigString(aggregated.Spec.SessionAffinityConfig), precedentCluster)))
	} else if c.serviceExportClient.hasCondition(serviceName, serviceNamespace, mcsv1a1.ServiceExportConflict,
		SessionAffinityConfigConflictReason) {
		conditions = append(conditions, newServiceExportCondition(
			mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, SessionAffinityConfigConflictReason, ""))
	}

	if aggregated.Spec.Type == mcsv1a1.ClusterSetIP && useClusterSetIP != nil {
		if aggregated.Annotations[constants.UseClustersetIP] != strconv.FormatBool(*useClusterSetIP) {
			clusterName := aggregated.Annotations[constants.ClustersetIPAllocatedBy]
			if clusterName == "" {
				clusterName = precedentCluster
			}

			conditions = append(conditions, newServiceExportCondition(mcsv1a1.ServiceExportConflict, corev1.ConditionTrue,
				ClusterSetIPEnablementConflictReason,
				fmt.Sprintf("The local service clusterset IP enablement setting %q conflicts with the enablement setting %q"+
					" determined by the first exporting cluster %q.",
					strconv.FormatBool(*useClusterSetIP), aggregated.Annotations[constants.UseClustersetIP], clusterName)))
		} else if c.serviceExportClient.hasCondition(serviceName, serviceNamespace, mcsv1a1.ServiceExportConflict,
			ClusterSetIPEnablementConflictReason) {
			conditions = append(conditions, newServiceExportCondition(
				mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, ClusterSetIPEnablementConflictReason, ""))
		}
	}

	c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace, conditions...)
}

func (c *ServiceImportController) updateAggregatedServiceImport(aggregated, local *mcsv1a1.ServiceImport) bool {
	oldestCluster := findClusterWithOldestTimestamp(aggregated.Annotations)
	if oldestCluster != sanitizeClusterID(c.clusterID) {
		return false
	}

	origSpec := aggregated.Spec

	aggregated.Spec.SessionAffinity = local.Spec.SessionAffinity
	aggregated.Spec.SessionAffinityConfig = local.Spec.SessionAffinityConfig

	return !reflect.DeepEqual(origSpec, aggregated.Spec)
}

func (c *ServiceImportController) localServiceImportLister(transform func(si *mcsv1a1.ServiceImport) runtime.Object) []runtime.Object {
	siList := c.localSyncer.ListResources()

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

func findClusterWithOldestTimestamp(from map[string]string) string {
	names := getClusterNamesOrderedByTimestamp(from)
	if len(names) > 0 {
		return names[0]
	}

	return ""
}

func toSessionAffinityConfigString(c *corev1.SessionAffinityConfig) string {
	if c != nil && c.ClientIP != nil && c.ClientIP.TimeoutSeconds != nil {
		return fmt.Sprintf("ClientIP TimeoutSeconds: %d", *c.ClientIP.TimeoutSeconds)
	}

	return "none"
}

func makeTimestampAnnotationKey(clusterID string) string {
	return timestampAnnotationPrefix + sanitizeClusterID(clusterID)
}

func sanitizeClusterID(clusterID string) string {
	if len(clusterID) > validation.DNS1123LabelMaxLength {
		clusterID = clusterID[:validation.DNS1123LabelMaxLength]
	}

	return resource.EnsureValidName(clusterID)
}
