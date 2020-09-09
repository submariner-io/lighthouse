package controller

import (
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	serviceUnavailable     = "ServiceUnavailable"
	invalidServiceType     = "UnsupportedServiceType"
	clusterIP              = "cluster-ip"
)

var MaxExportStatusConditions = 10

func New(spec *AgentSpecification, cfg *rest.Config) (*Controller, error) {
	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building clientset: %s", err.Error())
	}

	lighthouseClient, err := lighthouseClientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building lighthouseClient %s", err.Error())
	}

	syncerConf := &broker.SyncerConfig{LocalRestConfig: cfg}

	restMapper, err := util.BuildRestMapper(cfg)
	if err != nil {
		return nil, err
	}

	localClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating dynamic client: %v", err)
	}

	return NewWithDetail(spec, syncerConf, restMapper, localClient, kubeClientSet, lighthouseClient, nil,
		func(config *broker.SyncerConfig) (*broker.Syncer, error) {
			return broker.NewSyncer(*config)
		})
}

// Constructor that takes additional detail. This is intended for unit tests.
func NewWithDetail(spec *AgentSpecification, syncerConf *broker.SyncerConfig, restMapper meta.RESTMapper, localClient dynamic.Interface,
	kubeClientSet kubernetes.Interface, lighthouseClient lighthouseClientset.Interface, scheme *runtime.Scheme,
	newSyncer func(*broker.SyncerConfig) (*broker.Syncer, error)) (*Controller, error) {
	agentController := &Controller{
		clusterID:        spec.ClusterID,
		namespace:        spec.Namespace,
		globalnetEnabled: spec.GlobalnetEnabled,
		kubeClientSet:    kubeClientSet,
		lighthouseClient: lighthouseClient,
	}

	svcExportResourceConfig := broker.ResourceConfig{
		LocalSourceNamespace:      metav1.NamespaceAll,
		LocalResourceType:         &lighthousev2a1.ServiceExport{},
		LocalTransform:            agentController.serviceExportToRemoteServiceImport,
		LocalOnSuccessfulSync:     agentController.onSuccessfulServiceImportSync,
		BrokerResourceType:        &lighthousev2a1.ServiceImport{},
		BrokerResourcesEquivalent: agentController.serviceImportEquivalent,
	}

	syncerConf.Scheme = scheme
	syncerConf.LocalNamespace = spec.Namespace
	syncerConf.ResourceConfigs = []broker.ResourceConfig{
		svcExportResourceConfig,
	}

	var err error
	agentController.serviceExportSyncer, err = newSyncer(syncerConf)
	if err != nil {
		return nil, err
	}

	agentController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service deletion",
		SourceClient:    localClient,
		SourceNamespace: metav1.NamespaceAll,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       agentController.serviceExportSyncer.GetBrokerFederator(),
		ResourceType:    &corev1.Service{},
		Transform:       agentController.serviceToRemoteServiceImport,
		Scheme:          scheme,
	})
	if err != nil {
		return nil, err
	}

	agentController.endpointSliceSyncer, err = newSyncer(&broker.SyncerConfig{
		LocalRestConfig:  syncerConf.LocalRestConfig,
		LocalNamespace:   metav1.NamespaceAll,
		LocalClusterID:   spec.ClusterID,
		BrokerRestConfig: syncerConf.BrokerRestConfig,
		BrokerNamespace:  syncerConf.BrokerNamespace,
		Scheme:           syncerConf.Scheme,
		ResourceConfigs: []broker.ResourceConfig{
			{

				LocalSourceNamespace: metav1.NamespaceAll,
				LocalResourceType:    &discovery.EndpointSlice{},
				LocalTransform:       agentController.validateEndpointSliceLocal,
				LocalResourcesEquivalent: func(obj1, obj2 *unstructured.Unstructured) bool {
					return false
				},
				BrokerResourceType: &discovery.EndpointSlice{},
				BrokerResourcesEquivalent: func(obj1, obj2 *unstructured.Unstructured) bool {
					return false
				},
				BrokerTransform: agentController.remoteEndpointSliceToLocal,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	agentController.endpointSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Endpoint events",
		SourceClient:        localClient,
		SourceNamespace:     metav1.NamespaceAll,
		Direction:           syncer.LocalToRemote,
		RestMapper:          restMapper,
		Federator:           agentController.serviceExportSyncer.GetBrokerFederator(),
		ResourceType:        &corev1.Endpoints{},
		Transform:           agentController.endpointToRemoteServiceImport,
		ResourcesEquivalent: agentController.endpointEquivalent,
		Scheme:              scheme,
	})

	if err != nil {
		return nil, err
	}

	return agentController, nil
}

func (a *Controller) Start(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Agent controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Starting syncer")

	if err := a.serviceExportSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.serviceSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.endpointSyncer.Start(stopCh); err != nil {
		return err
	}

	if err := a.endpointSliceSyncer.Start(stopCh); err != nil {
		return err
	}

	klog.Info("Lighthouse agent syncer started")

	return nil
}

func (a *Controller) serviceExportToRemoteServiceImport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	if op == syncer.Update {
		return nil, false
	}

	svcExport := obj.(*lighthousev2a1.ServiceExport)
	serviceImport := a.newServiceImport(svcExport)

	if op == syncer.Delete {
		return serviceImport, false
	}

	svc, err := a.kubeClientSet.CoreV1().Services(svcExport.Namespace).Get(svcExport.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't exist", svcExport.Namespace, svcExport.Name)
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse, serviceUnavailable, "Service to be exported doesn't exist")

		return nil, true
	} else if err != nil {
		// some other error. Log and requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionUnknown, "ServiceRetrievalFailed", fmt.Sprintf("Error retrieving the Service: %v", err))
		klog.Errorf("Error retrieving Service (%s/%s): %v", svcExport.Namespace, svcExport.Name, err)
		return nil, true
	}

	svcType, ok := getServiceImportType(svc)

	if !ok {
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse, invalidServiceType, fmt.Sprintf("Service of type %v not supported", svc.Spec.Type))
		klog.Errorf("Service type %q not supported", svc.Spec.Type)

		return nil, false
	}

	if a.globalnetEnabled && svcType == lighthousev2a1.Headless {
		klog.Infof("Headless Services not supported with globalnet yet")
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse, invalidServiceType, "Headless Services not supported with Globalnet IP")

		return nil, false
	}

	if a.globalnetEnabled && getGlobalIpFromService(svc) == "" {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't have a global IP yet", svcExport.Namespace, svcExport.Name)

		// Globalnet enabled but service doesn't have globalIp yet, Update the status and requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse, "ServiceGlobalIPUnavailable", "Service doesn't have a global IP yet")

		return nil, true
	}

	serviceImport.Spec = lighthousev2a1.ServiceImportSpec{
		Type: svcType,
	}

	ips, err := a.getIPsForService(svc, svcType)
	if err != nil {
		// Failed to get ips for some reason, requeue
		a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionUnknown, "ServiceRetrievalFailed", err.Error())

		return nil, true
	}

	serviceImport.Status = lighthousev2a1.ServiceImportStatus{
		Clusters: []lighthousev2a1.ClusterStatus{
			{
				Cluster: a.clusterID,
				IPs:     ips,
			},
		},
	}

	if svcType == lighthousev2a1.ClusterSetIP {
		/* We also store the clusterIP in an annotation as an optimization to recover it in case the IPs are
		cleared out when here's no backing Endpoint pods.
		*/
		serviceImport.Annotations[clusterIP] = serviceImport.Status.Clusters[0].IPs[0]
	}

	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
		corev1.ConditionTrue, "AwaitingSync", "Awaiting sync of the ServiceImport to the broker")

	return serviceImport, false
}

func getServiceImportType(service *corev1.Service) (lighthousev2a1.ServiceImportType, bool) {
	if service.Spec.Type != "" && service.Spec.Type != corev1.ServiceTypeClusterIP {
		return "", false
	}

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		return lighthousev2a1.Headless, true
	}

	return lighthousev2a1.ClusterSetIP, true
}

func (a *Controller) onSuccessfulServiceImportSync(synced runtime.Object, op syncer.Operation) {
	if op != syncer.Create {
		return
	}

	serviceImport := synced.(*lighthousev2a1.ServiceImport)

	a.updateExportedServiceStatus(serviceImport.GetAnnotations()[lhconstants.OriginName],
		serviceImport.GetAnnotations()[lhconstants.OriginNamespace],
		lighthousev2a1.ServiceExportExported, corev1.ConditionTrue,
		"", "Service was successfully synced to the broker")
}

func (a *Controller) serviceToRemoteServiceImport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	if op != syncer.Delete {
		// Ignore create/update
		return nil, false
	}

	svc := obj.(*corev1.Service)
	svcExport, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(svc.Namespace).Get(svc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// Service Export not created yet
		return nil, false
	} else if err != nil {
		// some other error. Log and requeue
		klog.Errorf("Error retrieving ServiceExport for Service (%s/%s): %v", svc.Namespace, svc.Name, err)
		return nil, true
	}

	serviceImport := a.newServiceImport(svcExport)

	// Update the status and requeue
	a.updateExportedServiceStatus(svcExport.Name, svcExport.Namespace, lighthousev2a1.ServiceExportInitialized,
		corev1.ConditionFalse, serviceUnavailable, "Service to be exported doesn't exist")

	return serviceImport, false
}

func (a *Controller) endpointToRemoteServiceImport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	ep := obj.(*corev1.Endpoints)
	svcExport, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(ep.Namespace).Get(ep.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// Service Export not created yet
		return nil, false
	} else if err != nil {
		// some other error. Log and requeue
		klog.Errorf("Error retrieving ServiceExport for Endpoints (%s/%s): %v", ep.Namespace, ep.Name, err)
		return nil, true
	}

	serviceImport, err := a.getServiceImport(svcExport)

	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Error retrieving ServiceImport for Endpoints (%s/%s): %v", ep.Namespace, ep.Name, err)
		}

		// Requeue
		return nil, true
	}

	if serviceImport.Spec.Type == lighthousev2a1.Headless && a.globalnetEnabled {
		return nil, false
	}

	ipList := getIPsFromEndpoint(ep)

	if serviceImport.Spec.Type == lighthousev2a1.ClusterSetIP && len(ipList) > 0 {
		/*
			When there's no healthy pods, the IPs in the Endpoint will become empty and
			thus we also clear the ServiceImport IPs to avoid clients sending a request
			to a cluster which has no active pods to handle the request.

			Once at least one backing Endpoint pod becomes healthy, we need to re-establish
			the ServiceImport IPs from the previously cached clusterIP annotation.
		*/
		ipList = []string{serviceImport.Annotations[clusterIP]}
	}

	oldStatus := serviceImport.Status.DeepCopy()

	serviceImport.Status = lighthousev2a1.ServiceImportStatus{
		Clusters: []lighthousev2a1.ClusterStatus{
			{
				Cluster: a.clusterID,
				IPs:     ipList,
			},
		},
	}

	if reflect.DeepEqual(oldStatus, &serviceImport.Status) {
		klog.V(log.DEBUG).Infof("Old and new cluster status are same")
		return nil, false
	}

	return serviceImport, false
}

func (a *Controller) updateExportedServiceStatus(name, namespace string, condType lighthousev2a1.ServiceExportConditionType,
	status corev1.ConditionStatus, reason, msg string) {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(namespace).Get(name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return err
		}

		now := metav1.Now()
		exportCondition := lighthousev2a1.ServiceExportCondition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: &now,
			Reason:             &reason,
			Message:            &msg,
		}

		numCond := len(toUpdate.Status.Conditions)
		if numCond > 0 && serviceExportConditionEqual(&toUpdate.Status.Conditions[numCond-1], &exportCondition) {
			klog.V(log.DEBUG).Infof("Last ServiceExportCondition for (%s/%s) is equal - not updating status: %#v",
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

		_, err = a.lighthouseClient.LighthouseV2alpha1().ServiceExports(toUpdate.Namespace).Update(toUpdate)

		return err
	})
	if retryErr != nil {
		klog.Errorf("Error updating status for ServiceExport (%s/%s): %v", namespace, name, retryErr)
	}
}

func (a *Controller) serviceImportEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	return syncer.DefaultResourcesEquivalent(obj1, obj2) &&
		equality.Semantic.DeepEqual(util.GetNestedField(obj1, "status"), util.GetNestedField(obj2, "status"))
}

func (a *Controller) endpointEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	return equality.Semantic.DeepEqual(util.GetNestedField(obj1, "subsets"),
		util.GetNestedField(obj2, "subsets"))
}

func serviceExportConditionEqual(c1, c2 *lighthousev2a1.ServiceExportCondition) bool {
	return c1.Type == c2.Type && c1.Status == c2.Status && reflect.DeepEqual(c1.Reason, c2.Reason) &&
		reflect.DeepEqual(c1.Message, c2.Message)
}

func (a *Controller) newServiceImport(svcExport *lighthousev2a1.ServiceExport) *lighthousev2a1.ServiceImport {
	return &lighthousev2a1.ServiceImport{
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

func (a *Controller) getServiceImport(svcExport *lighthousev2a1.ServiceExport) (*lighthousev2a1.ServiceImport, error) {
	siName := a.getObjectNameWithClusterId(svcExport.Name, svcExport.Namespace)
	serviceImport, err := a.lighthouseClient.LighthouseV2alpha1().ServiceImports(a.namespace).Get(siName, metav1.GetOptions{})

	if err != nil {
		return nil, err
	}

	return serviceImport, nil
}

func (a *Controller) getIPsForService(service *corev1.Service, siType lighthousev2a1.ServiceImportType) ([]string, error) {
	if siType == lighthousev2a1.ClusterSetIP {
		mcsIp := getGlobalIpFromService(service)
		if mcsIp == "" {
			mcsIp = service.Spec.ClusterIP
		}

		return []string{mcsIp}, nil
	}

	endpoint, err := a.kubeClientSet.CoreV1().Endpoints(service.Namespace).Get(service.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Error retrieving Endpoints for Service (%s/%s): %v", service.Namespace, service.Name, err)
			return nil, errors.WithMessage(err, "Error retrieving the Endpoints for the Service")
		}

		return make([]string, 0), nil
	}

	return getIPsFromEndpoint(endpoint), nil
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

func (a *Controller) remoteEndpointSliceToLocal(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	labels := endpointSlice.GetObjectMeta().GetLabels()

	return &discovery.EndpointSlice{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpointSlice.Name,
			Namespace: labels[lhconstants.LabelSourceNamespace],
			Labels:    labels,
		},
		AddressType: endpointSlice.AddressType,
		Endpoints:   endpointSlice.Endpoints,
		Ports:       endpointSlice.Ports,
	}, false
}

func (a *Controller) validateEndpointSliceLocal(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	labels := endpointSlice.GetObjectMeta().GetLabels()

	if labels[discovery.LabelManagedBy] != lhconstants.LabelValueManagedBy {
		return nil, false
	}

	return obj, false
}
