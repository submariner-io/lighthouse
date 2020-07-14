package controller

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
)

func New(spec *AgentSpecification, cfg *rest.Config) (*Controller, error) {
	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building clientset: %s", err.Error())
	}

	lighthouseClient, err := lighthouseClientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building lighthouseClient %s", err.Error())
	}

	agentController := &Controller{
		clusterID:        spec.ClusterID,
		globalnetEnabled: spec.GlobalnetEnabled,
		kubeClientSet:    clientSet,
		lighthouseClient: lighthouseClient,
	}

	svcExportResourceConfig := broker.ResourceConfig{
		LocalSourceNamespace: metav1.NamespaceAll,
		LocalResourceType:    &lighthousev2a1.ServiceExport{},
		LocalTransform:       agentController.serviceExportToRemoteServiceImport,
		BrokerResourceType:   &lighthousev2a1.ServiceImport{},
	}

	syncerConf := broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalNamespace:  spec.Namespace,
		ResourceConfigs: []broker.ResourceConfig{
			svcExportResourceConfig,
		},
	}

	agentController.serviceExportSyncer, err = broker.NewSyncer(syncerConf)
	if err != nil {
		return nil, err
	}

	restMapper, err := util.BuildRestMapper(cfg)
	if err != nil {
		return nil, err
	}

	localClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating dynamic client: %v", err)
	}

	agentController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service deletion",
		SourceClient:    localClient,
		SourceNamespace: metav1.NamespaceAll,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       agentController.serviceExportSyncer.GetBrokerFederatorFor(svcExportResourceConfig.LocalResourceType),
		ResourceType:    &corev1.Service{},
		Transform:       agentController.serviceToRemoteServiceImport,
	})

	if err != nil {
		return nil, err
	}

	return agentController, nil
}

func (a *Controller) Run(stopCh <-chan struct{}) error {
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
	<-stopCh
	klog.Info("Lighthouse Agent stopping")
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
		a.updateExportedServiceStatus(svcExport, "Service to be exported doesn't exist", lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse)
		return nil, true
	} else if err != nil {
		// some other error. Log and requeue
		a.updateExportedServiceStatus(svcExport, fmt.Sprintf("Error obtaining service: %v", err), lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse)
		klog.Errorf("Unable to get service for %#v: %v", svc, err)
		return nil, true
	}
	if a.globalnetEnabled && getGlobalIpFromService(svc) == "" {
		// Globalnet enabled but service doesn't have globalIp yet, Update the status and requeue
		a.updateExportedServiceStatus(svcExport, "Service doesn't have global IP yet", lighthousev2a1.ServiceExportInitialized,
			corev1.ConditionFalse)
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
	a.updateExportedServiceStatus(svcExport, "Service was successfully synced to the broker",
		lighthousev2a1.ServiceExportExported, corev1.ConditionTrue)

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
	a.updateExportedServiceStatus(svcExport, "Service to be exported doesn't exist", lighthousev2a1.ServiceExportInitialized,
		corev1.ConditionFalse)

	return serviceImport, false
}

func (a *Controller) updateExportedServiceStatus(export *lighthousev2a1.ServiceExport, msg string,
	conditionType lighthousev2a1.ServiceExportConditionType, status corev1.ConditionStatus) {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(export.Namespace).Get(export.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			klog.Infof("Export Service not found, hence ignoring %v", export)
			return nil
		} else if err != nil {
			return err
		}
		exportCondtion := lighthousev2a1.ServiceExportCondition{
			Type:               conditionType,
			Status:             status,
			LastTransitionTime: nil,
			Reason:             nil,
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
