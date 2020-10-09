package controller

import (
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newServiceImportController(spec *AgentSpecification, serviceSyncer syncer.Interface, restMapper meta.RESTMapper,
	localClient dynamic.Interface, scheme *runtime.Scheme) (*ServiceImportController, error) {
	controller := &ServiceImportController{
		serviceSyncer: serviceSyncer,
		localClient:   localClient,
		restMapper:    restMapper,
		clusterID:     spec.ClusterID,
		scheme:        scheme,
	}

	var err error

	controller.serviceImportSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceImport watcher",
		SourceClient:    localClient,
		SourceNamespace: spec.Namespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       federate.NewNoopFederator(),
		ResourceType:    &mcsv1a1.ServiceImport{},
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
			value.(*EndpointController).stop()
			return true
		})

		klog.Infof("ServiceImport Controller stopped")
	}()

	if err := c.serviceImportSyncer.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(serviceImport *mcsv1a1.ServiceImport, key string) bool {
	if _, found := c.endpointControllers.Load(key); found {
		klog.V(log.DEBUG).Infof("The endpoint controller is already running for %q", key)
		return false
	}

	if serviceImport.Spec.Type != mcsv1a1.Headless ||
		serviceImport.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		return false
	}

	annotations := serviceImport.ObjectMeta.Annotations
	serviceNameSpace := annotations[lhconstants.OriginNamespace]
	serviceName := annotations[lhconstants.OriginName]

	obj, found, err := c.serviceSyncer.GetResource(serviceName, serviceNameSpace)
	if err != nil {
		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, err)

		return true
	}

	if !found {
		return false
	}

	service := obj.(*corev1.Service)
	if service.Spec.Selector == nil {
		klog.Errorf("The service %s/%s without a Selector is not supported", serviceNameSpace, serviceName)
		return false
	}

	endpointController, err := startEndpointController(c.localClient, c.restMapper, c.scheme,
		serviceImport.ObjectMeta.UID, serviceImport.ObjectMeta.Name, serviceNameSpace, serviceName, c.clusterID)
	if err != nil {
		klog.Errorf(err.Error())
		return true
	}

	c.endpointControllers.Store(key, endpointController)

	return false
}

func (c *ServiceImportController) serviceImportDeleted(serviceImport *mcsv1a1.ServiceImport, key string) bool {
	if serviceImport.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		return false
	}

	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.stop()
		c.endpointControllers.Delete(key)
	}

	return false
}

func (c *ServiceImportController) serviceImportToEndpointController(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	serviceImport := obj.(*mcsv1a1.ServiceImport)
	key, _ := cache.MetaNamespaceKeyFunc(serviceImport)
	if op == syncer.Create || op == syncer.Update {
		return nil, c.serviceImportCreatedOrUpdated(serviceImport, key)
	}

	return nil, c.serviceImportDeleted(serviceImport, key)
}

func (c *ServiceImportController) getServiceImport(name, namespace string) (*mcsv1a1.ServiceImport, bool, error) {
	obj, found, err := c.serviceImportSyncer.GetResource(name, namespace)
	if obj != nil {
		return obj.(*mcsv1a1.ServiceImport), found, err
	}

	return nil, found, err
}
