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
	"reflect"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	validations "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	serviceUnavailable = "ServiceUnavailable"
	invalidServiceType = "UnsupportedServiceType"
	clusterIP          = "cluster-ip"
)

type AgentConfig struct {
	ServiceImportCounterName string
	ServiceExportCounterName string
}

var logger = log.Logger{Logger: logf.Log.WithName("agent")}

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func New(spec *AgentSpecification, syncerConf broker.SyncerConfig, syncerMetricNames AgentConfig) (*Controller, error) {
	if errs := validations.IsDNS1123Label(spec.ClusterID); len(errs) > 0 {
		return nil, errors.Errorf("%s is not a valid ClusterID %v", spec.ClusterID, errs)
	}

	agentController := &Controller{
		clusterID:        spec.ClusterID,
		namespace:        spec.Namespace,
		globalnetEnabled: spec.GlobalnetEnabled,
	}

	_, gvr, err := util.ToUnstructuredResource(&mcsv1a1.ServiceExport{}, syncerConf.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	agentController.serviceExportClient = syncerConf.LocalClient.Resource(*gvr)

	syncerConf.LocalNamespace = spec.Namespace
	syncerConf.LocalClusterID = spec.ClusterID

	syncerConf.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace:  metav1.NamespaceAll,
			LocalResourceType:     &mcsv1a1.ServiceImport{},
			LocalTransform:        agentController.onLocalServiceImport,
			LocalOnSuccessfulSync: agentController.onSuccessfulServiceImportSync,
			BrokerResourceType:    &mcsv1a1.ServiceImport{},
			SyncCounterOpts: &prometheus.GaugeOpts{
				Name: syncerMetricNames.ServiceImportCounterName,
				Help: "Count of imported services",
			},
		},
	}

	agentController.serviceImportSyncer, err = broker.NewSyncer(syncerConf)
	if err != nil {
		return nil, errors.Wrap(err, "error creating ServiceImport syncer")
	}

	syncerConf.LocalNamespace = metav1.NamespaceAll
	syncerConf.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			LocalResourceType:    &discovery.EndpointSlice{},
			LocalTransform:       agentController.filterLocalEndpointSlices,
			LocalResourcesEquivalent: func(obj1, obj2 *unstructured.Unstructured) bool {
				return false
			},
			BrokerResourceType: &discovery.EndpointSlice{},
			BrokerResourcesEquivalent: func(obj1, obj2 *unstructured.Unstructured) bool {
				return false
			},
			BrokerTransform: agentController.remoteEndpointSliceToLocal,
		},
	}

	agentController.endpointSliceSyncer, err = broker.NewSyncer(syncerConf)
	if err != nil {
		return nil, errors.Wrap(err, "error creating EndpointSlice syncer")
	}

	agentController.serviceExportSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceExport -> ServiceImport",
		SourceClient:    syncerConf.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		RestMapper:      syncerConf.RestMapper,
		Federator:       agentController.serviceImportSyncer.GetLocalFederator(),
		ResourceType:    &mcsv1a1.ServiceExport{},
		Transform:       agentController.serviceExportToServiceImport,
		ResourcesEquivalent: func(oldObj, newObj *unstructured.Unstructured) bool {
			return !agentController.shouldProcessServiceExportUpdate(oldObj, newObj)
		},
		Scheme: syncerConf.Scheme,
		SyncCounterOpts: &prometheus.GaugeOpts{
			Name: syncerMetricNames.ServiceExportCounterName,
			Help: "Count of exported services",
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating ServiceExport syncer")
	}

	agentController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service deletion",
		SourceClient:    syncerConf.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		RestMapper:      syncerConf.RestMapper,
		Federator:       agentController.serviceImportSyncer.GetLocalFederator(),
		ResourceType:    &corev1.Service{},
		Transform:       agentController.serviceToRemoteServiceImport,
		Scheme:          syncerConf.Scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating Service syncer")
	}

	agentController.serviceImportController, err = newServiceImportController(spec, agentController.serviceSyncer,
		syncerConf.RestMapper, syncerConf.LocalClient, syncerConf.Scheme)
	if err != nil {
		return nil, err
	}

	return agentController, nil
}

func (a *Controller) Start(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting Agent controller")

	if err := a.serviceExportSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ServiceExport syncer")
	}

	if err := a.serviceSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting Service syncer")
	}

	if err := a.endpointSliceSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting EndpointSlice syncer")
	}

	if err := a.serviceImportSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ServiceImport syncer")
	}

	if err := a.serviceImportController.start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ServiceImport controller")
	}

	// This function also checks the legacy source name label for migration - this can be removed after 0.15.
	serviceImportSourceName := func(serviceImport *mcsv1a1.ServiceImport) string {
		name, ok := serviceImport.Labels[mcsv1a1.LabelServiceName]
		if ok {
			return name
		}

		return serviceImport.GetLabels()["lighthouse.submariner.io/sourceName"]
	}

	a.serviceExportSyncer.Reconcile(func() []runtime.Object {
		return a.serviceImportLister(func(si *mcsv1a1.ServiceImport) runtime.Object {
			return &mcsv1a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceImportSourceName(si),
					Namespace: si.GetLabels()[constants.LabelSourceNamespace],
				},
			}
		})
	})

	a.serviceSyncer.Reconcile(func() []runtime.Object {
		return a.serviceImportLister(func(si *mcsv1a1.ServiceImport) runtime.Object {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceImportSourceName(si),
					Namespace: si.GetLabels()[constants.LabelSourceNamespace],
				},
			}
		})
	})

	logger.Info("Agent controller started")

	return nil
}

func (a *Controller) serviceImportLister(transform func(si *mcsv1a1.ServiceImport) runtime.Object) []runtime.Object {
	siList, err := a.serviceImportSyncer.ListLocalResources(&mcsv1a1.ServiceImport{})
	if err != nil {
		logger.Error(err, "Error listing serviceImports")
		return nil
	}

	retList := make([]runtime.Object, 0, len(siList))

	for _, obj := range siList {
		si := obj.(*mcsv1a1.ServiceImport)

		retList = append(retList, transform(si))
	}

	return retList
}

func (a *Controller) serviceExportToServiceImport(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	svcExport := obj.(*mcsv1a1.ServiceExport)

	logger.V(log.DEBUG).Infof("ServiceExport %s/%s %sd", svcExport.Namespace, svcExport.Name, op)

	if op == syncer.Delete {
		return a.newServiceImport(svcExport.Name, svcExport.Namespace), false
	}

	obj, found, err := a.serviceSyncer.GetResource(svcExport.Name, svcExport.Namespace)
	if err != nil {
		// some other error. Log and requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid, corev1.ConditionUnknown,
			"ServiceRetrievalFailed", fmt.Sprintf("Error retrieving the Service: %v", err))
		logger.Errorf(err, "Error retrieving Service %s/%s", svcExport.Namespace, svcExport.Name)

		return nil, true
	}

	if !found {
		logger.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't exist", svcExport.Namespace, svcExport.Name)
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid, corev1.ConditionFalse,
			serviceUnavailable, "Service to be exported doesn't exist")

		return nil, false
	}

	svc := obj.(*corev1.Service)

	svcType, ok := getServiceImportType(svc)

	if !ok {
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid, corev1.ConditionFalse,
			invalidServiceType, fmt.Sprintf("Service of type %v not supported", svc.Spec.Type))
		logger.Errorf(nil, "Service type %q not supported for Service (%s/%s)", svc.Spec.Type, svcExport.Namespace, svcExport.Name)

		err = a.serviceImportSyncer.GetLocalFederator().Delete(a.newServiceImport(svcExport.Name, svcExport.Namespace))
		if err == nil || apierrors.IsNotFound(err) {
			return nil, false
		}

		logger.Errorf(nil, "Error deleting ServiceImport for Service (%s/%s)", svcExport.Namespace, svcExport.Name)

		return nil, true
	}

	serviceImport := a.newServiceImport(svcExport.Name, svcExport.Namespace)

	serviceImport.Spec = mcsv1a1.ServiceImportSpec{
		Ports:                 []mcsv1a1.ServicePort{},
		Type:                  svcType,
		SessionAffinityConfig: new(corev1.SessionAffinityConfig),
	}

	serviceImport.Status = mcsv1a1.ServiceImportStatus{
		Clusters: []mcsv1a1.ClusterStatus{
			{
				Cluster: a.clusterID,
			},
		},
	}

	if svcType == mcsv1a1.ClusterSetIP {
		if a.globalnetEnabled {
			ip, reason, msg := a.getGlobalIP(svc)
			if ip == "" {
				logger.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't have a global IP yet",
					svcExport.Namespace, svcExport.Name)
				// Globalnet enabled but service doesn't have globalIp yet, Update the status and requeue
				a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
					corev1.ConditionFalse, reason, msg)

				return nil, true
			}

			serviceImport.Spec.IPs = []string{ip}
		} else {
			serviceImport.Spec.IPs = []string{svc.Spec.ClusterIP}
		}

		serviceImport.Spec.Ports = a.getPortsForService(svc)
		/* We also store the clusterIP in an annotation as an optimization to recover it in case the IPs are
		cleared out when here's no backing Endpoint pods.
		*/
		serviceImport.Annotations[clusterIP] = serviceImport.Spec.IPs[0]
	}

	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid, corev1.ConditionTrue, "", "")

	logger.V(log.DEBUG).Infof("Returning ServiceImport : %#v", serviceImport)

	return serviceImport, false
}

func getServiceImportType(service *corev1.Service) (mcsv1a1.ServiceImportType, bool) {
	if service.Spec.Type != "" && service.Spec.Type != corev1.ServiceTypeClusterIP {
		return "", false
	}

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		return mcsv1a1.Headless, true
	}

	return mcsv1a1.ClusterSetIP, true
}

func (a *Controller) onLocalServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)

	if op == syncer.Delete {
		a.updateExportedServiceStatus(serviceImport.Labels[mcsv1a1.LabelServiceName],
			serviceImport.Labels[constants.LabelSourceNamespace], constants.ServiceExportSynced, corev1.ConditionFalse,
			"NoServiceImport", "ServiceImport was deleted")

		return obj, false
	}

	serviceName, ok := serviceImport.Labels[mcsv1a1.LabelServiceName]
	if !ok {
		// The label is missing - most likely b/c the ServiceImport hasn't yet been migrated from the legacy labels.
		logger.Infof("Label %q missing from ServiceImport (%s/%s) - not syncing", mcsv1a1.LabelServiceName,
			serviceImport.Namespace, serviceImport.Name)

		return nil, false
	}

	a.updateExportedServiceStatus(serviceName,
		serviceImport.Labels[constants.LabelSourceNamespace], constants.ServiceExportSynced, corev1.ConditionFalse,
		"AwaitingSync", fmt.Sprintf("ServiceImport %sd - awaiting sync to the broker", op))

	return obj, false
}

func (a *Controller) onSuccessfulServiceImportSync(synced runtime.Object, op syncer.Operation) {
	if op == syncer.Delete {
		return
	}

	serviceImport := synced.(*mcsv1a1.ServiceImport)

	a.updateExportedServiceStatus(serviceImport.Labels[mcsv1a1.LabelServiceName],
		serviceImport.Labels[constants.LabelSourceNamespace], constants.ServiceExportSynced, corev1.ConditionTrue, "",
		"ServiceImport was successfully synced to the broker")
}

func (a *Controller) shouldProcessServiceExportUpdate(oldObj, newObj *unstructured.Unstructured) bool {
	oldValidCond := FindServiceExportStatusCondition(a.toServiceExport(oldObj).Status.Conditions, mcsv1a1.ServiceExportValid)
	newValidCond := FindServiceExportStatusCondition(a.toServiceExport(newObj).Status.Conditions, mcsv1a1.ServiceExportValid)

	if newValidCond != nil && !reflect.DeepEqual(oldValidCond, newValidCond) && newValidCond.Status == corev1.ConditionFalse {
		return true
	}

	return false
}

func FindServiceExportStatusCondition(conditions []mcsv1a1.ServiceExportCondition,
	condType mcsv1a1.ServiceExportConditionType,
) *mcsv1a1.ServiceExportCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}

	return nil
}

func (a *Controller) serviceToRemoteServiceImport(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	svc := obj.(*corev1.Service)

	obj, found, err := a.serviceExportSyncer.GetResource(svc.Name, svc.Namespace)
	if err != nil {
		// some other error. Log and requeue
		logger.Errorf(err, "Error retrieving ServiceExport for Service (%s/%s)", svc.Namespace, svc.Name)
		return nil, true
	}

	if !found {
		// Service Export not created yet
		return nil, false
	}

	if op == syncer.Create || op == syncer.Update {
		return a.serviceExportToServiceImport(obj, numRequeues, op)
	}

	svcExport := obj.(*mcsv1a1.ServiceExport)

	serviceImport := a.newServiceImport(svcExport.Name, svcExport.Namespace)

	// Update the status and requeue
	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid, corev1.ConditionFalse,
		serviceUnavailable, "Service to be exported doesn't exist")

	return serviceImport, false
}

func (a *Controller) updateExportedServiceStatus(name, namespace string, condType mcsv1a1.ServiceExportConditionType,
	status corev1.ConditionStatus, reason, msg string,
) {
	logger.V(log.DEBUG).Infof("updateExportedServiceStatus for (%s/%s) - Type: %q, Status: %q, Reason: %q, Message: %q",
		namespace, name, condType, status, reason, msg)

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.getServiceExport(name, namespace)
		if apierrors.IsNotFound(err) {
			logger.Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return err
		}

		now := metav1.Now()
		newCondition := mcsv1a1.ServiceExportCondition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: &now,
			Reason:             &reason,
			Message:            &msg,
		}

		prevCond := FindServiceExportStatusCondition(toUpdate.Status.Conditions, condType)
		if prevCond == nil {
			toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, newCondition)
		} else if serviceExportConditionEqual(prevCond, &newCondition) {
			logger.V(log.TRACE).Infof("Last ServiceExportCondition for (%s/%s) is equal - not updating status: %#v",
				namespace, name, toUpdate.Status.Conditions[0])
			return nil
		} else {
			*prevCond = newCondition
		}

		raw, err := resource.ToUnstructured(toUpdate)
		if err != nil {
			return errors.Wrap(err, "error converting resource")
		}

		_, err = a.serviceExportClient.Namespace(toUpdate.Namespace).UpdateStatus(context.TODO(), raw, metav1.UpdateOptions{})

		return errors.Wrap(err, "error from UpdateStatus")
	})

	if retryErr != nil {
		logger.Errorf(retryErr, "Error updating status for ServiceExport (%s/%s)", namespace, name)
	}
}

func (a *Controller) getServiceExport(name, namespace string) (*mcsv1a1.ServiceExport, error) {
	obj, err := a.serviceExportClient.Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving ServiceExport")
	}

	return a.toServiceExport(obj), nil
}

func (a *Controller) toServiceExport(obj *unstructured.Unstructured) *mcsv1a1.ServiceExport {
	se := &mcsv1a1.ServiceExport{}

	err := a.serviceImportController.scheme.Convert(obj, se, nil)
	if err != nil {
		panic(errors.WithMessagef(err, "Error converting %#v to ServiceExport", obj))
	}

	return se
}

func serviceExportConditionEqual(c1, c2 *mcsv1a1.ServiceExportCondition) bool {
	return c1.Type == c2.Type && c1.Status == c2.Status && reflect.DeepEqual(c1.Reason, c2.Reason) &&
		reflect.DeepEqual(c1.Message, c2.Message)
}

func (a *Controller) newServiceImport(name, namespace string) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:        a.getObjectNameWithClusterID(name, namespace),
			Annotations: map[string]string{},
			Labels: map[string]string{
				mcsv1a1.LabelServiceName:        name,
				constants.LabelSourceNamespace:  namespace,
				constants.MCSLabelSourceCluster: a.clusterID,
			},
		},
	}
}

func (a *Controller) getPortsForService(service *corev1.Service) []mcsv1a1.ServicePort {
	mcsPorts := make([]mcsv1a1.ServicePort, 0, len(service.Spec.Ports))

	for _, port := range service.Spec.Ports {
		mcsPorts = append(mcsPorts, mcsv1a1.ServicePort{
			Name:     port.Name,
			Protocol: port.Protocol,
			Port:     port.Port,
		})
	}

	return mcsPorts
}

func (a *Controller) getObjectNameWithClusterID(name, namespace string) string {
	return name + "-" + namespace + "-" + a.clusterID
}

func (a *Controller) remoteEndpointSliceToLocal(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	endpointSlice.Namespace = endpointSlice.GetObjectMeta().GetLabels()[constants.LabelSourceNamespace]

	return endpointSlice, false
}

func (a *Controller) filterLocalEndpointSlices(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	labels := endpointSlice.GetObjectMeta().GetLabels()

	if labels[discovery.LabelManagedBy] != constants.LabelValueManagedBy {
		return nil, false
	}

	oldName := labels[mcsv1a1.LabelServiceName] + "-" + labels[constants.MCSLabelSourceCluster]
	if op != syncer.Delete && endpointSlice.Name == oldName {
		logger.Infof("EndpointSlice %s/%s has the old naming convention sans namespace - deleting it",
			endpointSlice.Namespace, endpointSlice.Name)

		err := a.endpointSliceSyncer.GetLocalFederator().Delete(endpointSlice)
		if err != nil {
			logger.Errorf(err, "Error deleting local EndpointSlice %s/%s", endpointSlice.Namespace, endpointSlice.Name)
		}

		return nil, false
	}

	return obj, false
}

func (a *Controller) getGlobalIP(service *corev1.Service) (ip, reason, msg string) {
	if a.globalnetEnabled {
		ingressIP, found := a.getIngressIP(service.Name, service.Namespace)
		if !found {
			return "", defaultReasonIPUnavailable, defaultMsgIPUnavailable
		}

		return ingressIP.allocatedIP, ingressIP.unallocatedReason, ingressIP.unallocatedMsg
	}

	return "", "GlobalnetDisabled", "Globalnet is not enabled"
}

func (a *Controller) getIngressIP(name, namespace string) (*IngressIP, bool) {
	obj, found := a.serviceImportController.globalIngressIPCache.getForService(namespace, name)
	if !found {
		return nil, false
	}

	return parseIngressIP(obj), true
}
