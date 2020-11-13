package controller

import (
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/mcs/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newLHServiceExportController(lighthouseClient lighthouseClientset.Interface,
	mcsClientSet mcsClientset.Interface) (*LHServiceExportController, error) {
	serviceExportController := LHServiceExportController{
		queue:            workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		mcsClientSet:     mcsClientSet,
		lighthouseClient: lighthouseClient,
	}

	return &serviceExportController, nil
}

func (c *LHServiceExportController) start(stopCh <-chan struct{}) error {
	informerFactory := externalversions.NewSharedInformerFactoryWithOptions(c.lighthouseClient, 0)
	c.serviceExportInformer = informerFactory.Lighthouse().V2alpha1().ServiceExports().Informer()

	c.serviceExportInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				var si *lighthousev2a1.ServiceExport
				var ok bool
				if si, ok = obj.(*lighthousev2a1.ServiceExport); !ok {
					tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
					if !ok {
						klog.Errorf("Failed to get deleted serviceexport object for key %s, serviceExport %v", key, si)
						return
					}

					si, ok = tombstone.Obj.(*lighthousev2a1.ServiceExport)

					if !ok {
						klog.Errorf("Failed to convert deleted tombstone object %v  to serviceexport", tombstone.Obj)
						return
					}
				}
				c.serviceExportDeletedMap.Store(key, si)
				c.queue.AddRateLimited(key)
			}
		},
	})

	go c.serviceExportInformer.Run(stopCh)
	go c.runServiceExportWorker()

	go func(stopCh <-chan struct{}) {
		<-stopCh
		c.queue.ShutDown()
	}(stopCh)

	return nil
}

func (c *LHServiceExportController) runServiceExportWorker() {
	for {
		keyObj, shutdown := c.queue.Get()
		if shutdown {
			return
		}

		key := keyObj.(string)

		func() {
			defer c.queue.Done(key)
			obj, exists, err := c.serviceExportInformer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving the object with store is  %v from the cache: %v", c.serviceExportInformer.GetIndexer().ListKeys(), err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)

				return
			}

			if exists {
				err = c.serviceExportCreated(obj, key)
			} else {
				err = c.serviceExportDeleted(key)
			}

			if err != nil {
				if !exists {
					c.serviceExportDeletedMap.Store(key, obj)
				}

				c.queue.AddRateLimited(key)
			} else {
				c.queue.Forget(key)
			}
		}()
	}
}

func (c *LHServiceExportController) serviceExportCreated(obj interface{}, key string) error {
	serviceExportCreated := obj.(*lighthousev2a1.ServiceExport)
	objMeta := serviceExportCreated.GetObjectMeta()
	mcsServiceExport := &mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: objMeta.GetName(),
		},
	}
	_, err := c.mcsClientSet.MulticlusterV1alpha1().ServiceExports(objMeta.GetNamespace()).Create(mcsServiceExport)
	if err != nil {
		return err
	}

	return nil
}

func (c *LHServiceExportController) serviceExportDeleted(key string) error {
	obj, found := c.serviceExportDeletedMap.Load(key)
	if !found {
		klog.Warningf("No endpoint controller found  for %q", key)
		return nil
	}

	c.serviceExportDeletedMap.Delete(key)

	se := obj.(lighthousev2a1.ServiceExport)

	err := c.mcsClientSet.MulticlusterV1alpha1().ServiceExports(se.Namespace).Delete(se.Name, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}
