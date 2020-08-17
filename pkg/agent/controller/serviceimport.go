package controller

import (
	"fmt"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

func NewServiceImportController(spec *AgentSpecification, cfg *rest.Config) (*ServiceImportController, error) {
	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building clientset: %s", err.Error())
	}

	lighthouseClient, err := lighthouseClientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building lighthouseClient %s", err.Error())
	}

	serviceImportController := ServiceImportController{
		queue:               workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		kubeClientSet:       kubeClientSet,
		lighthouseClient:    lighthouseClient,
		clusterID:           spec.ClusterID,
		lighthouseNamespace: spec.Namespace,
	}

	return &serviceImportController, nil
}

func (c *ServiceImportController) Start(stopCh <-chan struct{}) error {
	informerFactory := externalversions.NewSharedInformerFactoryWithOptions(c.lighthouseClient, 0,
		externalversions.WithNamespace(c.lighthouseNamespace))
	c.serviceInformer = informerFactory.Lighthouse().V2alpha1().ServiceImports().Informer()

	c.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.V(2).Infof("ServiceImport %q added", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			// TODO Change level
			klog.Infof("ServiceImport %q updated", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.Infof("ServiceImport %q deleted", key)
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
				c.endpointControllerDeleted.Store(si, key)
				c.queue.AddRateLimited(key)
			}
		},
	})

	go c.serviceInformer.Run(stopCh)
	go c.runServiceImportWorker()

	go func(stopCh <-chan struct{}) {
		<-stopCh
		c.queue.ShutDown()

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

			c.queue.Forget(key)

			if exists {
				c.serviceImportCreatedOrUpdated(obj, key)
			} else {
				c.serviceImportDeleted(key)
			}
		}()
	}
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(obj interface{}, key string) {
	if _, found := c.endpointControllerCreated.Load(key); found {
		klog.V(2).Infof("The endpointController is already running for  key %q", key)
		return
	}

	serviceImportCreated := obj.(*lighthousev2a1.ServiceImport)
	if serviceImportCreated.Spec.Type != lighthousev2a1.Headless {
		return
	}

	annotations := serviceImportCreated.ObjectMeta.Annotations
	serviceNameSpace := annotations[originNamespace]
	serviceName := annotations[originName]
	var service *corev1.Service
	var isFound bool

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		service, err = c.kubeClientSet.CoreV1().Services(serviceNameSpace).Get(serviceName, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				isFound = false
			}
			return err
		}
		isFound = true
		return nil
	})

	if retryErr != nil || !isFound {
		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, retryErr)
		return
	}

	if service.Spec.Selector == nil {
		klog.Errorf("The service %q in the namespace %q without selector is not supported", serviceName, serviceNameSpace)
		return
	}

	labelSelector := labels.Set(service.Spec.Selector).AsSelector()
	endpointController, err := NewEndpointController(c.kubeClientSet, serviceImportCreated.ObjectMeta.UID,
		serviceImportCreated.ObjectMeta.Name, c.clusterID)

	if err != nil {
		klog.Errorf("Creating endpoint controller for service %q in the namespace %q failed", serviceName, serviceNameSpace)
		return
	}

	err = endpointController.Start(endpointController.stopCh, labelSelector)
	if err != nil {
		klog.Errorf("Starting endpoint controller for service %q in the namespace %q failed", serviceName, serviceNameSpace)
		return
	}

	c.endpointControllerCreated.Store(key, endpointController)
}

func (c *ServiceImportController) serviceImportDeleted(key string) {
	obj, found := c.endpointControllerDeleted.Load(key)
	if !found {
		klog.Errorf("deleting endpointSlice for key %s failed since object not found", key)
		return
	}

	c.endpointControllerDeleted.Delete(key)

	si := obj.(lighthousev2a1.ServiceImport)
	matchLabels := si.ObjectMeta.Labels
	labelSelector := labels.Set(map[string]string{"app": matchLabels["app"]}).AsSelector()
	err := c.kubeClientSet.DiscoveryV1beta1().EndpointSlices(si.Namespace).
		DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector.String()})
	if err != nil && !errors.IsNotFound(err) {
		c.endpointControllerDeleted.Store(key, si)
		c.queue.AddRateLimited(key)

		return
	}

	if obj, found := c.endpointControllerCreated.Load(key); found {
		endpointController := obj.(*EndpointController)
		endpointController.endPointqueue.ShutDown()
		close(endpointController.stopCh)
		c.endpointControllerCreated.Delete(key)
	}
}
