package controller

import (
	"github.com/submariner-io/admiral/pkg/log"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

func newServiceImportController(spec *AgentSpecification, kubeClientSet kubernetes.Interface,
	lighthouseClient lighthouseClientset.Interface) *ServiceImportController {
	return &ServiceImportController{
		queue:            workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		kubeClientSet:    kubeClientSet,
		lighthouseClient: lighthouseClient,
		clusterID:        spec.ClusterID,
		namespace:        spec.Namespace,
	}
}

func (c *ServiceImportController) start(stopCh <-chan struct{}) error {
	informerFactory := externalversions.NewSharedInformerFactoryWithOptions(c.lighthouseClient, 0,
		externalversions.WithNamespace(c.namespace))
	c.serviceInformer = informerFactory.Lighthouse().V2alpha1().ServiceImports().Informer()

	c.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.V(log.DEBUG).Infof("ServiceImport %q added", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.V(log.DEBUG).Infof("ServiceImport %q updated", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.V(log.DEBUG).Infof("ServiceImport %q deleted", key)
			if err == nil {
				var si *lighthousev2a1.ServiceImport
				var ok bool
				if si, ok = obj.(*lighthousev2a1.ServiceImport); !ok {
					tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
					if !ok {
						klog.Errorf("Failed to get deleted serviceimport object for key %s, serviceImport %v", key, si)
						return
					}

					si, ok = tombstone.Obj.(*lighthousev2a1.ServiceImport)

					if !ok {
						klog.Errorf("Failed to convert deleted tombstone object %v  to serviceimport", tombstone.Obj)
						return
					}
				}
				if si.Spec.Type != lighthousev2a1.Headless {
					return
				}
				c.serviceImportDeletedMap.Store(key, si)
				c.queue.AddRateLimited(key)
			}
		},
	})

	go c.serviceInformer.Run(stopCh)
	go c.runServiceImportWorker()

	go func(stopCh <-chan struct{}) {
		<-stopCh
		c.queue.ShutDown()

		c.endpointControllers.Range(func(key, value interface{}) bool {
			value.(*EndpointController).Stop()
			return true
		})

		klog.Infof("ServiceImport Controller stopped")
	}(stopCh)

	return nil
}

func (c *ServiceImportController) runServiceImportWorker() {
	for {
		keyObj, shutdown := c.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for ServiceImports stopped")
			return
		}

		key := keyObj.(string)

		func() {
			defer c.queue.Done(key)
			obj, exists, err := c.serviceInformer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving the object with store is  %v from the cache: %v", c.serviceInformer.GetIndexer().ListKeys(), err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)

				return
			}

			if exists {
				err = c.serviceImportCreatedOrUpdated(obj, key)
			} else {
				err = c.serviceImportDeleted(key)
			}

			if err != nil {
				c.queue.AddRateLimited(key)
			} else {
				c.queue.Forget(key)
			}
		}()
	}
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(obj interface{}, key string) error {
	if _, found := c.endpointControllers.Load(key); found {
		klog.V(log.DEBUG).Infof("The endpoint controller is already running fof %q", key)
		return nil
	}

	serviceImportCreated := obj.(*lighthousev2a1.ServiceImport)
	if serviceImportCreated.Spec.Type != lighthousev2a1.Headless ||
		serviceImportCreated.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		return nil
	}

	annotations := serviceImportCreated.ObjectMeta.Annotations
	serviceNameSpace := annotations[lhconstants.OriginNamespace]
	serviceName := annotations[lhconstants.OriginName]
	var service *corev1.Service

	service, err := c.kubeClientSet.CoreV1().Services(serviceNameSpace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, err)

		return err
	}

	if service.Spec.Selector == nil {
		klog.Errorf("The service %s/%s without a Selector is not supported", serviceNameSpace, serviceName)
		return nil
	}

	labelSelector := labels.Set(service.GetLabels()).AsSelector()
	endpointController := newEndpointController(c.kubeClientSet, serviceImportCreated.ObjectMeta.UID,
		serviceImportCreated.ObjectMeta.Name, serviceNameSpace, c.clusterID)

	endpointController.start(endpointController.stopCh, labelSelector)
	c.endpointControllers.Store(key, endpointController)

	return nil
}

func (c *ServiceImportController) serviceImportDeleted(key string) error {
	obj, found := c.serviceImportDeletedMap.Load(key)
	if !found {
		klog.Warningf("No endpoint controller found  for %q", key)
		c.serviceImportDeletedMap.Delete(key)

		return nil
	}

	si := obj.(*lighthousev2a1.ServiceImport)

	if si.GetLabels()[lhconstants.LabelSourceCluster] != c.clusterID {
		c.serviceImportDeletedMap.Delete(key)
		return nil
	}

	if obj, found := c.endpointControllers.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.Stop()
		c.endpointControllers.Delete(key)
	}

	c.serviceImportDeletedMap.Delete(key)

	return nil
}
