package controller

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	agentController := &Controller{
		clusterID:     spec.ClusterID,
		restConfig:    cfg,
		kubeClientSet: clientSet,
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
	return mcs
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
