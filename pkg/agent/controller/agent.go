package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
)

var excludeSvcList = map[string]bool{"kubernetes": true, "openshift": true}

func New(spec *AgentSpecification, cfg *rest.Config) (*Controller, error) {
	exclusionNSMap := map[string]bool{"kube-system": true, "submariner": true, "openshift": true,
		"submariner-operator": true, "kubefed-operator": true}
	for _, v := range spec.ExcludeNS {
		exclusionNSMap[v] = true
	}
	agentController := &Controller{
		clusterID:         spec.ClusterID,
		restConfig:        cfg,
		excludeNamespaces: exclusionNSMap,
	}
	svcResourceConfig := broker.ResourceConfig{
		LocalSourceNamespace: metav1.NamespaceAll,
		LocalResourceType:    &corev1.Service{},
		LocalTransform:       agentController.localServiceToRemoteMcs,
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

func (a *Controller) localServiceToRemoteMcs(obj runtime.Object) runtime.Object {
	svc := obj.(*corev1.Service)
	if !a.isValidService(svc.Namespace, svc.Name) {
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

func (a *Controller) isValidService(namespace string, serviceName string) bool {
	if a.excludeNamespaces[namespace] || excludeSvcList[serviceName] {
		return false
	}
	return true
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
