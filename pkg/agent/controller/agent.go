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
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	serviceUnavailable     = "ServiceUnavailable"
	invalidServiceType     = "UnsupportedServiceType"
	clusterIP              = "cluster-ip"
)

var MaxExportStatusConditions = 10

func New(spec *AgentSpecification, syncerConf broker.SyncerConfig, kubeClientSet kubernetes.Interface) (*Controller, error) {
	agentController := &Controller{
		clusterID:        spec.ClusterID,
		namespace:        spec.Namespace,
		globalnetEnabled: spec.GlobalnetEnabled,
		kubeClientSet:    kubeClientSet,
	}

	_, gvr, err := util.ToUnstructuredResource(&mcsv1a1.ServiceExport{}, syncerConf.RestMapper)
	if err != nil {
		return nil, err
	}

	agentController.serviceExportClient = syncerConf.LocalClient.Resource(*gvr)

	syncerConf.LocalNamespace = spec.Namespace
	syncerConf.LocalClusterID = spec.ClusterID

	syncerConf.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			LocalResourceType:    &mcsv1a1.ServiceImport{},
			BrokerResourceType:   &mcsv1a1.ServiceImport{},
		},
	}

	agentController.serviceImportSyncer, err = broker.NewSyncer(syncerConf)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	agentController.serviceExportSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:             "ServiceExport -> ServiceImport",
		SourceClient:     syncerConf.LocalClient,
		SourceNamespace:  metav1.NamespaceAll,
		Direction:        syncer.RemoteToLocal,
		RestMapper:       syncerConf.RestMapper,
		Federator:        agentController.serviceImportSyncer.GetLocalFederator(),
		ResourceType:     &mcsv1a1.ServiceExport{},
		Transform:        agentController.serviceExportToServiceImport,
		OnSuccessfulSync: agentController.onSuccessfulServiceImportSync,
		Scheme:           syncerConf.Scheme,
	})
	if err != nil {
		return nil, err
	}

	agentController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service deletion",
		SourceClient:    syncerConf.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		Direction:       syncer.RemoteToLocal,
		RestMapper:      syncerConf.RestMapper,
		Federator:       agentController.serviceImportSyncer.GetLocalFederator(),
		ResourceType:    &corev1.Service{},
		Transform:       agentController.serviceToRemoteServiceImport,
		Scheme:          syncerConf.Scheme,
	})
	if err != nil {
		return nil, err
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
	klog.Info("Starting Agent controller")

	if err := a.serviceExportSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.serviceSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.endpointSliceSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.serviceImportSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.serviceImportController.start(stopCh); err != nil {
		return err
	}

	klog.Info("Agent controller started")

	return nil
}

func (a *Controller) serviceExportToServiceImport(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	svcExport := obj.(*mcsv1a1.ServiceExport)

	if op == syncer.Delete {
		return a.newServiceImport(svcExport), false
	}

	obj, found, err := a.serviceSyncer.GetResource(svcExport.Name, svcExport.Namespace)
	if err != nil {
		// some other error. Log and requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionUnknown, "ServiceRetrievalFailed", fmt.Sprintf("Error retrieving the Service: %v", err))
		klog.Errorf("Error retrieving Service (%s/%s): %v", svcExport.Namespace, svcExport.Name, err)

		return nil, true
	}

	if !found {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't exist", svcExport.Namespace, svcExport.Name)
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionFalse, serviceUnavailable, "Service to be exported doesn't exist")

		return nil, true
	}

	if op == syncer.Update && getLastExportConditionReason(svcExport) != serviceUnavailable {
		return nil, false
	}

	svc := obj.(*corev1.Service)

	svcType, ok := getServiceImportType(svc)

	if !ok {
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionFalse, invalidServiceType, fmt.Sprintf("Service of type %v not supported", svc.Spec.Type))
		klog.Errorf("Service type %q not supported", svc.Spec.Type)

		return nil, false
	}

	if a.globalnetEnabled && svcType == mcsv1a1.Headless {
		klog.Infof("Headless Services not supported with globalnet yet")
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionFalse, invalidServiceType, "Headless Services not supported with Globalnet IP")

		return nil, false
	}

	if a.globalnetEnabled && getGlobalIpFromService(svc) == "" {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't have a global IP yet", svcExport.Namespace, svcExport.Name)

		// Globalnet enabled but service doesn't have globalIp yet, Update the status and requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionFalse, "ServiceGlobalIPUnavailable", "Service doesn't have a global IP yet")

		return nil, true
	}

	serviceImport := a.newServiceImport(svcExport)

	serviceImport.Spec = mcsv1a1.ServiceImportSpec{
		Ports:                 []mcsv1a1.ServicePort{},
		Type:                  svcType,
		SessionAffinityConfig: new(corev1.SessionAffinityConfig),
	}

	ips, ports, err := a.getIPsAndPortsForService(svc, svcType)
	if err != nil {
		// Failed to get ips for some reason, requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
			corev1.ConditionUnknown, "ServiceRetrievalFailed", err.Error())

		return nil, true
	}

	serviceImport.Status = mcsv1a1.ServiceImportStatus{
		Clusters: []mcsv1a1.ClusterStatus{
			{
				Cluster: a.clusterID,
			},
		},
	}

	if svcType == mcsv1a1.ClusterSetIP {
		serviceImport.Spec.IPs = ips
		serviceImport.Spec.Ports = ports
		/* We also store the clusterIP in an annotation as an optimization to recover it in case the IPs are
		cleared out when here's no backing Endpoint pods.
		*/
		serviceImport.Annotations[clusterIP] = ips[0]
	}

	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
		corev1.ConditionFalse, "AwaitingSync", "Awaiting sync of the ServiceImport to the broker")

	return serviceImport, false
}

func getLastExportConditionReason(svcExport *mcsv1a1.ServiceExport) string {
	numCond := len(svcExport.Status.Conditions)
	if numCond > 0 && svcExport.Status.Conditions[numCond-1].Reason != nil {
		return *svcExport.Status.Conditions[numCond-1].Reason
	}

	return ""
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

func (a *Controller) onSuccessfulServiceImportSync(synced runtime.Object, op syncer.Operation) {
	if op == syncer.Delete {
		return
	}

	serviceImport := synced.(*mcsv1a1.ServiceImport)

	a.updateExportedServiceStatus(serviceImport.GetAnnotations()[lhconstants.OriginName],
		serviceImport.GetAnnotations()[lhconstants.OriginNamespace],
		mcsv1a1.ServiceExportValid, corev1.ConditionTrue,
		"", "Service was successfully synced to the broker")
}

func (a *Controller) serviceToRemoteServiceImport(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	if op != syncer.Delete {
		// Ignore create/update
		return nil, false
	}

	svc := obj.(*corev1.Service)
	obj, found, err := a.serviceExportSyncer.GetResource(svc.Name, svc.Namespace)
	if err != nil {
		// some other error. Log and requeue
		klog.Errorf("Error retrieving ServiceExport for Service (%s/%s): %v", svc.Namespace, svc.Name, err)
		return nil, true
	}

	if !found {
		// Service Export not created yet
		return nil, false
	}

	svcExport := obj.(*mcsv1a1.ServiceExport)

	serviceImport := a.newServiceImport(svcExport)

	// Update the status and requeue
	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, mcsv1a1.ServiceExportValid,
		corev1.ConditionFalse, serviceUnavailable, "Service to be exported doesn't exist")

	return serviceImport, false
}

func (a *Controller) updateExportedServiceStatus(name, namespace string, condType mcsv1a1.ServiceExportConditionType,
	status corev1.ConditionStatus, reason, msg string) {
	klog.V(log.DEBUG).Infof("updateExportedServiceStatus for (%s/%s) - Type: %q, Status: %q, Reason: %q, Message: %q",
		namespace, name, condType, status, reason, msg)

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.getServiceExport(name, namespace)
		if apierrors.IsNotFound(err) {
			klog.Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return err
		}

		now := metav1.Now()
		exportCondition := mcsv1a1.ServiceExportCondition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: &now,
			Reason:             &reason,
			Message:            &msg,
		}

		numCond := len(toUpdate.Status.Conditions)
		if numCond > 0 && serviceExportConditionEqual(&toUpdate.Status.Conditions[numCond-1], &exportCondition) {
			klog.V(log.TRACE).Infof("Last ServiceExportCondition for (%s/%s) is equal - not updating status: %#v",
				namespace, name, toUpdate.Status.Conditions[numCond-1])
			return nil
		}

		if numCond >= MaxExportStatusConditions {
			copy(toUpdate.Status.Conditions[0:], toUpdate.Status.Conditions[1:])
			toUpdate.Status.Conditions = toUpdate.Status.Conditions[:MaxExportStatusConditions]
			toUpdate.Status.Conditions[MaxExportStatusConditions-1] = exportCondition
		} else {
			toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, exportCondition)
		}

		raw, err := util.ToUnstructured(toUpdate)
		if err != nil {
			return err
		}

		_, err = a.serviceExportClient.Namespace(toUpdate.Namespace).UpdateStatus(raw, metav1.UpdateOptions{})

		return err
	})
	if retryErr != nil {
		klog.Errorf("Error updating status for ServiceExport (%s/%s): %v", namespace, name, retryErr)
	}
}

func (a *Controller) getServiceExport(name, namespace string) (*mcsv1a1.ServiceExport, error) {
	obj, err := a.serviceExportClient.Namespace(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	se := &mcsv1a1.ServiceExport{}
	err = a.serviceImportController.scheme.Convert(obj, se, nil)
	if err != nil {
		return nil, errors.WithMessagef(err, "Error converting %#v to ServiceExport", obj)
	}

	return se, nil
}

func serviceExportConditionEqual(c1, c2 *mcsv1a1.ServiceExportCondition) bool {
	return c1.Type == c2.Type && c1.Status == c2.Status && reflect.DeepEqual(c1.Reason, c2.Reason) &&
		reflect.DeepEqual(c1.Message, c2.Message)
}

func (a *Controller) newServiceImport(svcExport *mcsv1a1.ServiceExport) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: a.getObjectNameWithClusterId(svcExport.Name, svcExport.Namespace),
			Annotations: map[string]string{
				lhconstants.OriginName:      svcExport.Name,
				lhconstants.OriginNamespace: svcExport.Namespace,
			},
			Labels: map[string]string{
				lhconstants.LabelSourceName:      svcExport.Name,
				lhconstants.LabelSourceNamespace: svcExport.Namespace,
				lhconstants.LabelSourceCluster:   a.clusterID,
			},
		},
	}
}

func (a *Controller) getIPsAndPortsForService(service *corev1.Service, siType mcsv1a1.ServiceImportType) (
	[]string, []mcsv1a1.ServicePort, error) {
	var mcsPorts []mcsv1a1.ServicePort

	for _, port := range service.Spec.Ports {
		mcsPorts = append(mcsPorts, mcsv1a1.ServicePort{
			Name:     port.Name,
			Protocol: port.Protocol,
			Port:     port.Port,
		})
	}

	if siType == mcsv1a1.ClusterSetIP {
		mcsIp := getGlobalIpFromService(service)
		if mcsIp == "" {
			mcsIp = service.Spec.ClusterIP
		}

		return []string{mcsIp}, mcsPorts, nil
	}

	endpoint, err := a.kubeClientSet.CoreV1().Endpoints(service.Namespace).Get(service.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Error retrieving Endpoints for Service (%s/%s): %v", service.Namespace, service.Name, err)
			return nil, nil, errors.WithMessage(err, "Error retrieving the Endpoints for the Service")
		}

		return make([]string, 0), nil, nil
	}

	return getIPsFromEndpoint(endpoint), mcsPorts, nil
}

func (a *Controller) getObjectNameWithClusterId(name, namespace string) string {
	return name + "-" + namespace + "-" + a.clusterID
}

func getIPsFromEndpoint(endpoint *corev1.Endpoints) []string {
	ipList := make([]string, 0)

	for _, eps := range endpoint.Subsets {
		for _, addr := range eps.Addresses {
			ipList = append(ipList, addr.IP)
		}
	}

	klog.V(log.DEBUG).Infof("IPList %v for endpoint (%s/%s)", ipList, endpoint.Namespace, endpoint.Name)

	return ipList
}

func getGlobalIpFromService(service *corev1.Service) string {
	if service != nil {
		annotations := service.GetAnnotations()
		if annotations != nil {
			return annotations[submarinerIpamGlobalIp]
		}
	}

	return ""
}

func (a *Controller) remoteEndpointSliceToLocal(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	endpointSlice.Namespace = endpointSlice.GetObjectMeta().GetLabels()[lhconstants.LabelSourceNamespace]

	return endpointSlice, false
}

func (a *Controller) filterLocalEndpointSlices(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	labels := endpointSlice.GetObjectMeta().GetLabels()

	if labels[discovery.LabelManagedBy] != lhconstants.LabelValueManagedBy {
		return nil, false
	}

	return obj, false
}
