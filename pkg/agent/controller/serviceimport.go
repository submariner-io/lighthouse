package controller

import (
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func newServiceImportController(spec *AgentSpecification, kubeClientSet kubernetes.Interface, restMapper meta.RESTMapper,
	localClient dynamic.Interface, scheme *runtime.Scheme) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		kubeClientSet: kubeClientSet,
		clusterID:     spec.ClusterID,
	}

	var err error

	controller.serviceImportSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceImport watcher",
		SourceClient:    localClient,
		SourceNamespace: spec.Namespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       federate.NewNoopFederator(),
		ResourceType:    &lighthousev2a1.ServiceImport{},
		Transform:       controller.serviceImportToEndpointController,
		Scheme:          scheme,
	})
	if err != nil {
		return nil, err
	}

	return controller, nil
}

func (c *ServiceImportController) start(stopCh <-chan struct{}) error {
	go func() {
		<-stopCh

		c.endpointControllers.Range(func(key, value interface{}) bool {
			value.(*EndpointController).Stop()
			return true
		})

		klog.Infof("ServiceImport Controller stopped")
	}()

	if err := c.serviceImportSyncer.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(serviceImport *lighthousev2a1.ServiceImport, key string) bool {
	if _, found := c.endpointControllers.Load(key); found {
		klog.V(log.DEBUG).Infof("The endpoint controller is already running for %q", key)
		return false
	}

	if serviceImport.Spec.Type != lighthousev2a1.Headless ||
		serviceImport.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		return false
	}

	annotations := serviceImport.ObjectMeta.Annotations
	serviceNameSpace := annotations[lhconstants.OriginNamespace]
	serviceName := annotations[lhconstants.OriginName]
	var service *corev1.Service

	service, err := c.kubeClientSet.CoreV1().Services(serviceNameSpace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false
		}

		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, err)

		return true
	}

	if service.Spec.Selector == nil {
		klog.Errorf("The service %s/%s without a Selector is not supported", serviceNameSpace, serviceName)
		return false
	}

	endpointController := newEndpointController(c.kubeClientSet, serviceImport.ObjectMeta.UID,
		serviceImport.ObjectMeta.Name, serviceName, serviceNameSpace, c.clusterID)

	endpointController.start(endpointController.stopCh)
	c.endpointControllers.Store(key, endpointController)

	return false
}

func (c *ServiceImportController) serviceImportDeleted(serviceImport *lighthousev2a1.ServiceImport, key string) bool {
	if serviceImport.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		return false
	}

	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.Stop()
		c.endpointControllers.Delete(key)
	}

	return false
}

func (c *ServiceImportController) serviceImportToEndpointController(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*lighthousev2a1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)
	if op == syncer.Create || op == syncer.Update {
		return nil, c.serviceImportCreatedOrUpdated(serviceImport, key)
	}

	return nil, c.serviceImportDeleted(serviceImport, key)
}
