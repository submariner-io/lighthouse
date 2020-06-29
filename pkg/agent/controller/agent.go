package controller

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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
		restConfig:       cfg,
		kubeClientSet:    clientSet,
		lighthouseClient: lighthouseClient,
	}
	svcResourceConfig := broker.ResourceConfig{
		LocalSourceNamespace: metav1.NamespaceAll,
		LocalResourceType:    &lighthousev2a1.ServiceExport{},
		LocalTransform:       agentController.serviceExportToRemoteMcs,
		BrokerResourceType:   &lighthousev1.MultiClusterService{},
	}
	syncerConf := broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalNamespace:  spec.Namespace,
		ResourceConfigs: []broker.ResourceConfig{
			svcResourceConfig,
		},
	}
	syncer, err := broker.NewSyncer(syncerConf)
	if err != nil {
		return nil, err
	}
	agentController.svcSyncer = syncer

	return agentController, nil
}

func (a *Controller) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Agent controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Starting syncer")
	if err := a.svcSyncer.Start(stopCh); err != nil {
		return err
	}
	klog.Info("Lighthouse agent syncer started")
	<-stopCh
	klog.Info("Lighthouse Agent stopping")
	return nil
}

func (a *Controller) serviceExportToRemoteMcs(obj runtime.Object) runtime.Object {
	svcExport := obj.(*lighthousev2a1.ServiceExport)
	svc, err := a.kubeClientSet.CoreV1().Services(svcExport.Namespace).Get(svcExport.Name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("No matching service for %v", svcExport)
		return nil
	}
	mcs := &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name: svc.Name + "-" + svc.Namespace + "-" + a.clusterID,
			Annotations: map[string]string{
				"origin-name":      svc.Name,
				"origin-namespace": svc.Namespace,
			},
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				a.newClusterServiceInfo(svc, a.clusterID),
			},
		},
	}
	err = a.updateExportedServiceStatus(svcExport)
	if err != nil {
		klog.Errorf("Updating status failed %v", svcExport)
	}
	return mcs
}

func (a *Controller) updateExportedServiceStatus(export *lighthousev2a1.ServiceExport) error {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		toUpdate, err := a.lighthouseClient.LighthouseV2alpha1().ServiceExports(export.Namespace).Get(export.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			klog.Infof("Export Service not found, hence ignoring %v", export)
			return nil
		} else if err != nil {
			klog.Errorf("Error retrieving the object %v", export)
			return err
		}
		msg := "Service written to the broker Successfully"
		exportCondtion := lighthousev2a1.ServiceExportCondition{
			Type:               lighthousev2a1.ServiceExportExported,
			Status:             corev1.ConditionTrue,
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
	return retryErr
}

func (a *Controller) newClusterServiceInfo(service *corev1.Service, clusterID string) lighthousev1.ClusterServiceInfo {
	mcsIp := getGlobalIpFromService(service)
	if mcsIp == "" {
		mcsIp = service.Spec.ClusterIP
	}
	return lighthousev1.ClusterServiceInfo{
		ClusterID: clusterID,
		ServiceIP: mcsIp,
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
