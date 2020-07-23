package controller

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
)

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
		globalnetEnabled: spec.GlobalnetEnabled,
		kubeClientSet:    kubeClientSet,
		lighthouseClient: lighthouseClient,
	}

	svcExportResourceConfig := broker.ResourceConfig{
		LocalSourceNamespace: metav1.NamespaceAll,
		LocalResourceType:    &lighthousev2a1.ServiceExport{},
		LocalTransform:       agentController.serviceExportToRemoteServiceImport,
		BrokerResourceType:   &lighthousev2a1.ServiceImport{},
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

	klog.Info("Lighthouse agent syncer started")

	return nil
}

func (a *Controller) serviceExportToRemoteServiceImport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	svcExport := obj.(*lighthousev2a1.ServiceExport)
	serviceImport := a.newServiceImport(svcExport)

	if op == syncer.Delete {
		return serviceImport, false
	}
	svc, err := a.kubeClientSet.CoreV1().Services(svcExport.Namespace).Get(svcExport.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't exist", svcExport.Namespace, svcExport.Name)
		a.updateExportedServiceStatus(svcExport, lighthousev2a1.ServiceExportInitialized, corev1.ConditionFalse,
			serviceUnavailable, "Service to be exported doesn't exist")
		return nil, true
	} else if err != nil {
		// some other error. Log and requeue
		a.updateExportedServiceStatus(svcExport, lighthousev2a1.ServiceExportInitialized, corev1.ConditionUnknown,
			"ServiceRetrievalFailed", fmt.Sprintf("Error retrieving the Service: %v", err))
		klog.Errorf("Unable to get service for %#v: %v", svc, err)
		return nil, true
	}
	if a.globalnetEnabled && getGlobalIpFromService(svc) == "" {
		klog.V(log.DEBUG).Infof("Service to be exported (%s/%s) doesn't have a global IP yet", svcExport.Namespace, svcExport.Name)

		// Globalnet enabled but service doesn't have globalIp yet, Update the status and requeue
		a.updateExportedServiceStatus(svcExport, lighthousev2a1.ServiceExportInitialized, corev1.ConditionFalse,
			"ServiceGlobalIPUnavailable", "Service doesn't have a global IP yet")
		return nil, true
	}
	serviceImport.Spec = lighthousev2a1.ServiceImportSpec{
		Type: lighthousev2a1.SuperclusterIP,
	}
	serviceImport.Status = lighthousev2a1.ServiceImportStatus{
		Clusters: []lighthousev2a1.ClusterStatus{
			a.newClusterStatus(svc, a.clusterID),
		},
	}
	a.updateExportedServiceStatus(svcExport, lighthousev2a1.ServiceExportExported, corev1.ConditionTrue,
		"", "Service was successfully synced to the broker")

	return serviceImport, false
}

func (a *Controller) serviceToRemoteServiceImport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	if op != syncer.Delete {
		// Ignore create/update
		return nil, false
	}

	svc := obj.(*corev1.Service)
	svcExport, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(svc.Namespace).Get(svc.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Service Export not created yet
		return nil, false
	} else if err != nil {
		// some other error. Log and requeue
		klog.Errorf("Unable to get ServiceExport for %#v: %v", svc, err)
		return nil, true
	}

	serviceImport := a.newServiceImport(svcExport)

	// Update the status and requeue
	a.updateExportedServiceStatus(svcExport, lighthousev2a1.ServiceExportInitialized, corev1.ConditionFalse,
		serviceUnavailable, "Service to be exported doesn't exist")

	return serviceImport, false
}

func (a *Controller) updateExportedServiceStatus(export *lighthousev2a1.ServiceExport, condType lighthousev2a1.ServiceExportConditionType,
	status corev1.ConditionStatus, reason, msg string) {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(export.Namespace).Get(export.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			klog.Infof("ServiceExport (%s/%s) not found - unable to update status", export.Namespace, export.Name)
			return nil
		} else if err != nil {
			return err
		}

		now := metav1.Now()
		exportCondtion := lighthousev2a1.ServiceExportCondition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: &now,
			Reason:             &reason,
			Message:            &msg,
		}

		toUpdate.Status = lighthousev2a1.ServiceExportStatus{
			Conditions: []lighthousev2a1.ServiceExportCondition{
				exportCondtion,
			},
		}

		_, err = a.lighthouseClient.LighthouseV2alpha1().ServiceExports(toUpdate.Namespace).Update(toUpdate)

		return err
	})
	if retryErr != nil {
		klog.Errorf("Error updating status for %#v: %v", export, retryErr)
	}
}

func (a *Controller) newClusterStatus(service *corev1.Service, clusterID string) lighthousev2a1.ClusterStatus {
	mcsIp := getGlobalIpFromService(service)
	if mcsIp == "" {
		mcsIp = service.Spec.ClusterIP
	}
	return lighthousev2a1.ClusterStatus{
		Cluster: clusterID,
		IPs:     []string{mcsIp},
	}
}

func (a *Controller) newServiceImport(svcExport *lighthousev2a1.ServiceExport) *lighthousev2a1.ServiceImport {
	return &lighthousev2a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: svcExport.Name + "-" + svcExport.Namespace + "-" + a.clusterID,
			Annotations: map[string]string{
				"origin-name":      svcExport.Name,
				"origin-namespace": svcExport.Namespace,
			},
		},
	}
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
