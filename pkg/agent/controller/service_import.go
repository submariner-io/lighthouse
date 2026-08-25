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
	"slices"
	"strconv"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/admiral/pkg/workqueue"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newServiceImportController(spec *AgentSpecification, agentConfig AgentConfig, syncerConfig broker.SyncerConfig,
	brokerClient dynamic.Interface, brokerNamespace string, serviceExportClient *ServiceExportClient,
	localLHEndpointSliceLister EndpointSliceListerFn, namespaceValidator *NamespaceValidator,
) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		localClient:                syncerConfig.LocalClient,
		brokerClient:               brokerClient,
		brokerNamespace:            brokerNamespace,
		restMapper:                 syncerConfig.RestMapper,
		clusterID:                  spec.ClusterID,
		localNamespace:             spec.Namespace,
		converter:                  converter{scheme: syncerConfig.Scheme},
		serviceExportClient:        serviceExportClient,
		localLHEndpointSliceLister: localLHEndpointSliceLister,
		clustersetIPPool:           agentConfig.IPPool,
		clustersetIPEnabled:        spec.ClustersetIPEnabled,
		supportedIPFamilies:        agentConfig.SupportedIPFamilies,
		namespaceValidator:         namespaceValidator,
	}

	localToBrokerFederator := federate.NewCreateOrUpdateFederator(federate.CreateOrUpdateOptions{
		Client:          brokerClient,
		RestMapper:      syncerConfig.RestMapper,
		TargetNamespace: brokerNamespace,
		IdentifyingLabels: []string{
			mcsv1b1.LabelServiceName,
			constants.LabelSourceNamespace,
			mcsv1b1.LabelSourceCluster,
		},
	})
	localToBrokerFederator.LogEvents("local -> broker")

	controller.localFederator = federate.NewCompositeFederator(&federate.FederatorFuncs{
		DistributeFunc: controller.createOrUpdateAggregate,
		DeleteFunc:     controller.updateAggregateOnDelete,
	}, localToBrokerFederator)

	var err error

	controller.localSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Local ServiceImport",
		SourceClient:    syncerConfig.LocalClient,
		SourceNamespace: controller.localNamespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      syncerConfig.RestMapper,
		Federator:       controller.localFederator,
		ResourceType:    &mcsv1b1.ServiceImport{},
		Transform:       controller.onLocalServiceImport,
		Scheme:          syncerConfig.Scheme,
		Metrics: syncer.MetricsConfig{
			SyncCounterOpts: &prometheus.GaugeOpts{
				Name: agentConfig.ServiceExportCounterName,
				Help: "Count of exported services",
			},
		},
		WorkQueueConfig: workqueue.ConfigFromGlobal("local-service-import", nil),
		MaxLogVerbosity: global.Get("local-service-import.syncer.max-verbosity", 0),
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating local ServiceImport syncer")
	}

	controller.remoteSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Remote ServiceImport",
		SourceClient:    brokerClient,
		SourceNamespace: brokerNamespace,
		RestMapper:      syncerConfig.RestMapper,
		Federator: federate.NewCreateOrUpdateFederator(federate.CreateOrUpdateOptions{
			Client:          syncerConfig.LocalClient,
			RestMapper:      syncerConfig.RestMapper,
			TargetNamespace: corev1.NamespaceAll,
		}),
		ResourceType:      &mcsv1b1.ServiceImport{},
		Transform:         controller.onRemoteServiceImport,
		OnSuccessfulSync:  controller.onSuccessfulSyncFromBroker,
		Scheme:            syncerConfig.Scheme,
		NamespaceInformer: syncerConfig.NamespaceInformer,
		Metrics: syncer.MetricsConfig{
			SyncCounterOpts: &prometheus.GaugeOpts{
				Name: agentConfig.ServiceImportCounterName,
				Help: "Count of imported services",
			},
		},
		WorkQueueConfig: workqueue.ConfigFromGlobal("remote-service-import", nil),
		MaxLogVerbosity: global.Get("remote-service-import.syncer.max-verbosity", 0),
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

func (c *ServiceImportController) start(ctx context.Context, stopCh <-chan struct{}) error {
	if c.globalIngressIPCache != nil {
		if err := c.globalIngressIPCache.start(stopCh); err != nil {
			return err
		}
	}

	go func() {
		<-stopCh

		c.endpointControllers.Range(func(_, value any) bool {
			err := value.(*ServiceEndpointSliceController).stop(context.TODO())
			if err != nil {
				logger.Warningf("Error stopping service EndpointSlice controller: %s", err)
			}

			return true
		})

		logger.Info("ServiceImport Controller stopped")
	}()

	if err := c.reserveAggregatedServiceImportIPs(ctx); err != nil {
		return err
	}

	if err := c.localSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting local ServiceImport syncer")
	}

	if err := c.remoteSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting remote ServiceImport syncer")
	}

	c.reconcileLocalAggregatedServiceImports(ctx)
	c.reconcileRemoteAggregatedServiceImports()
	c.reconcileLocalServiceImportsOnBroker()

	return nil
}

func (c *ServiceImportController) isIPInClustersetCIDR(si *mcsv1b1.ServiceImport) bool {
	if c.clustersetIPPool == nil || len(si.Spec.IPs) == 0 {
		return false
	}

	ip := net.ParseIP(si.Spec.IPs[0])
	_, cidr, _ := net.ParseCIDR(c.clustersetIPPool.GetCIDR())

	return ip != nil && cidr.Contains(ip)
}

func (c *ServiceImportController) reserveAggregatedServiceImportIPs(ctx context.Context) error {
	client := c.localClient.Resource(serviceImportGVR).Namespace(corev1.NamespaceAll)

	list, err := client.List(ctx, metav1.ListOptions{})
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

func (c *ServiceImportController) reconcileLocalServiceImportsOnBroker() {
	c.localSyncer.Reconcile(func() []runtime.Object {
		siList := c.remoteSyncer.ListResources()
		retList := make([]runtime.Object, 0, len(siList))

		for i := range siList {
			si := c.converter.toServiceImport(siList[i])

			if si.Annotations[mcsv1b1.LabelServiceName] != "" ||
				si.Labels[mcsv1b1.LabelSourceCluster] != c.clusterID {
				// This is an aggregated ServiceImport or another cluster's local ServiceImport.
				continue
			}

			si.Namespace = c.localNamespace
			si.Name = si.Labels[mcsv1b1.LabelServiceName] + "-" + si.Labels[constants.LabelSourceNamespace] + "-" + c.clusterID

			retList = append(retList, si)
		}

		return retList
	})
}

func (c *ServiceImportController) startEndpointsController(ctx context.Context, serviceImport *mcsv1b1.ServiceImport) error {
	key := localEndpointsControllerKey(serviceImport)

	if obj, found := c.endpointControllers.Load(key); found {
		logger.Infof("Stopping previous EndpointSlice controller for %q", key)

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

func (c *ServiceImportController) stopEndpointsController(ctx context.Context, key string) error {
	if obj, found := c.endpointControllers.Load(key); found {
		var err error
		endpointController := obj.(*ServiceEndpointSliceController)

		err = endpointController.stop(ctx)
		if err == nil {
			err = endpointController.cleanup(ctx)
			if err == nil {
				c.endpointControllers.Delete(key)
			}
		}

		return err
	}

	return nil
}

func (c *ServiceImportController) onLocalServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1b1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)
	ctx := context.TODO()

	serviceName := serviceImportSourceName(serviceImport)

	if serviceImport.Labels[mcsv1b1.LabelSourceCluster] != c.clusterID {
		return nil, false
	}

	logger.Infof("Local ServiceImport %q %sd", key, op)

	if op == syncer.Delete {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionReady,
				metav1.ConditionFalse, ServiceExportReasonNoServiceImport, "ServiceImport was deleted"))
	} else if op == syncer.Create {
		c.serviceExportClient.tryUpdateStatusConditions(ctx, serviceName, serviceImport.Labels[constants.LabelSourceNamespace],
			false, mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionReady, metav1.ConditionFalse,
				mcsv1b1.ServiceExportReasonPending, fmt.Sprintf("ServiceImport %sd - awaiting aggregation on the broker", op)))
	}

	return c.transformLocalToBroker(serviceImport), false
}

func (c *ServiceImportController) transformLocalToBroker(serviceImport *mcsv1b1.ServiceImport) *mcsv1b1.ServiceImport {
	// Prepare the local ServiceImport for sync to the broker.
	serviceImport.Name = ""
	serviceImport.GenerateName = serviceImportSourceName(serviceImport) + "-"
	serviceImport.Status = mcsv1b1.ServiceImportStatus{}

	if serviceImport.Annotations == nil {
		serviceImport.Annotations = map[string]string{}
	}

	serviceImport.Annotations[constants.UseClustersetIP] = strconv.FormatBool(c.determineUseClusterSetIP(serviceImport))

	return serviceImport
}

func (c *ServiceImportController) onRemoteServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1b1.ServiceImport)

	ctx := context.TODO()

	serviceName, ok := serviceImport.Annotations[mcsv1b1.LabelServiceName]
	if ok {
		// This is an aggregated ServiceImport - sync it to the local service namespace.
		serviceImport.Name = serviceName
		targetNamespace := serviceImport.Annotations[constants.LabelSourceNamespace]

		if err := c.namespaceValidator.CheckAllowed(targetNamespace); err != nil {
			logger.Warningf("Rejecting aggregated ServiceImport %q: %v", serviceName, err)

			// Do not delete local resources based on rejected broker objects - they cannot be trusted.
			// Stale local resources should be cleaned up by administrators.
			return nil, false
		}

		serviceImport.Namespace = targetNamespace

		delete(serviceImport.Annotations, mcsv1b1.LabelServiceName)
		delete(serviceImport.Annotations, constants.LabelSourceNamespace)

		ready := metav1.Condition{
			Type:   string(mcsv1b1.ServiceImportConditionReady),
			Status: metav1.ConditionTrue,
			Reason: string(mcsv1b1.ServiceImportReasonReady),
		}

		// Check if any of the ServiceImport's IPFamilies are supported
		if !slices.ContainsFunc(serviceImport.Spec.IPFamilies, func(ipFamily corev1.IPFamily) bool {
			return slices.Contains(c.supportedIPFamilies, ipFamily)
		}) {
			ready.Status = metav1.ConditionFalse
			ready.Reason = string(mcsv1b1.ServiceImportReasonIPFamilyNotSupported)
			ready.Message = fmt.Sprintf("Service IP families %v are not compatible with the importing cluster IP families %v. "+
				"The service will not be accessible from this cluster", serviceImport.Spec.IPFamilies, c.supportedIPFamilies)
		}

		if meta.SetStatusCondition(&serviceImport.Status.Conditions, ready) {
			logger.Infof("Set status condition for imported ServiceImport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
				serviceImport.Namespace, serviceImport.Name, ready.Type, ready.Status, ready.Reason, ready.Message)
		}

		return serviceImport, false
	}

	serviceName = serviceImport.Labels[mcsv1b1.LabelServiceName]
	serviceNamespace := serviceImport.Labels[constants.LabelSourceNamespace]

	localServiceExport := c.serviceExportClient.getLocalInstance(serviceName, serviceNamespace)
	if localServiceExport == nil {
		return nil, false
	}

	aggregatedObj, err := c.brokerServiceImportClient().Get(ctx,
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

	logger.Infof("ServiceImport %q from cluster %q %sd on broker",
		key, serviceImport.Labels[mcsv1b1.LabelSourceCluster], op)

	precedentServiceImport := c.checkForConflicts(ctx, aggregatedServiceImport)
	if precedentServiceImport == nil {
		return nil, false
	}

	isPrecedentCluster := precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster] == c.clusterID
	if isPrecedentCluster {
		err = c.updateAggregate(ctx, serviceName, serviceNamespace,
			func(aggregated *mcsv1b1.ServiceImport) error {
				aggregated.Spec.SessionAffinity = precedentServiceImport.Spec.SessionAffinity
				aggregated.Spec.SessionAffinityConfig = precedentServiceImport.Spec.SessionAffinityConfig
				aggregated.Spec.TrafficDistribution = precedentServiceImport.Spec.TrafficDistribution
				aggregated.Spec.InternalTrafficPolicy = precedentServiceImport.Spec.InternalTrafficPolicy
				aggregated.Spec.Ports = precedentServiceImport.Spec.Ports
				aggregated.Spec.IPFamilies = precedentServiceImport.Spec.IPFamilies

				return nil
			})
		if err != nil {
			logger.Errorf(err, "Error updating aggregated ServiceImport \"%s%s\"", serviceNamespace, serviceName)
		}
	}

	return nil, err != nil
}

func (c *ServiceImportController) onSuccessfulSyncFromBroker(synced runtime.Object, op syncer.Operation) bool {
	aggregatedServiceImport := synced.(*mcsv1b1.ServiceImport)

	if op == syncer.Delete {
		if c.isIPInClustersetCIDR(aggregatedServiceImport) {
			_ = c.clustersetIPPool.Release(aggregatedServiceImport.Spec.IPs[0])
		}
	}

	return false
}

func (c *ServiceImportController) determineUseClusterSetIP(localServiceImport *mcsv1b1.ServiceImport) bool {
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

func (c *ServiceImportController) localServiceImportLister(transform func(si *mcsv1b1.ServiceImport) runtime.Object) []runtime.Object {
	siList := c.localSyncer.ListResources()

	retList := make([]runtime.Object, 0, len(siList))

	for _, obj := range siList {
		si := obj.(*mcsv1b1.ServiceImport)

		if si.Labels[mcsv1b1.LabelSourceCluster] != c.clusterID {
			continue
		}

		retList = append(retList, transform(si))
	}

	return retList
}

func serviceImportSourceName(serviceImport *mcsv1b1.ServiceImport) string {
	return serviceImport.Labels[mcsv1b1.LabelServiceName]
}

func localEndpointsControllerKey(si *mcsv1b1.ServiceImport) string {
	return si.Labels[constants.LabelSourceNamespace] + "/" + si.Labels[mcsv1b1.LabelServiceName]
}
