package serviceimport

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	lighthouseInformers "github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type Controller struct {
	// Indirection hook for unit tests to supply fake client sets
	newClientset    func(kubeConfig *rest.Config) (lighthouseClientset.Interface, error)
	serviceInformer cache.SharedIndexInformer
	queue           workqueue.RateLimitingInterface
	stopCh          chan struct{}
	serviceImports  *Map
}

func NewController(remoteServiceMap *Map) *Controller {
	return &Controller{
		queue: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		newClientset: func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return lighthouseClientset.NewForConfig(c)
		},
		serviceImports: remoteServiceMap,
		stopCh:         make(chan struct{}),
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting ServiceImport Controller")

	clientSet, err := c.newClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("Error creating client set: %v", err)
	}

	informerFactory := lighthouseInformers.NewSharedInformerFactoryWithOptions(clientSet, 0,
		lighthouseInformers.WithNamespace(metav1.NamespaceAll))

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
				c.serviceImportDeleted(obj, key)
			}
		},
	})

	go c.serviceInformer.Run(c.stopCh)
	go c.runWorker()

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)
	c.queue.ShutDown()

	klog.Infof("ServiceImport Controller stopped")
}

func (c *Controller) runWorker() {
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
				klog.Errorf("Error retrieving service with key %q from the cache: %v", key, err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)
				return
			}

			if exists {
				c.serviceImportCreatedOrUpdated(obj, key)
			}

			c.queue.Forget(key)
		}()
	}
}

func (c *Controller) serviceImportCreatedOrUpdated(obj interface{}, key string) {
	klog.V(log.DEBUG).Infof("In serviceImportCreatedOrUpdated for key %q, service: %#v, ", key, obj)

	c.serviceImports.Put(obj.(*lighthousev2a1.ServiceImport))
}

func (c *Controller) serviceImportDeleted(obj interface{}, key string) {
	var mcs *lighthousev2a1.ServiceImport
	var ok bool
	if mcs, ok = obj.(*lighthousev2a1.ServiceImport); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Failed to get deleted serviceimport object for %s", key)
			return
		}
		mcs, ok = tombstone.Obj.(*lighthousev2a1.ServiceImport)
		if !ok {
			klog.Errorf("Failed to convert deleted tombstone object %v  to serviceimport", tombstone.Obj)
			return
		}
	}
	c.serviceImports.Remove(mcs)
}
