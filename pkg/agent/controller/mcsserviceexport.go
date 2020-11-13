package controller

import (
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/mcs/client/clientset/versioned"
	"github.com/submariner-io/lighthouse/pkg/mcs/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newMCSServiceExportController(lighthouseClient lighthouseClientset.Interface,
	mcsClientSet mcsClientset.Interface) (*MCSServiceExportController, error) {
	serviceExportController := MCSServiceExportController{
		queue:            workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		mcsClientSet:     mcsClientSet,
		lighthouseClient: lighthouseClient,
	}

	return &serviceExportController, nil
}

func (c *MCSServiceExportController) start(stopCh <-chan struct{}) error {
	informerFactory := externalversions.NewSharedInformerFactoryWithOptions(c.mcsClientSet, 0)
	c.serviceExportInformer = informerFactory.Multicluster().V1alpha1().ServiceExports().Informer()

	c.serviceExportInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {

		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
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

func (c *MCSServiceExportController) runServiceExportWorker() {
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
				klog.Errorf("Error retrieving the object with store is  %v from the cache: %v",
					c.serviceExportInformer.GetIndexer().ListKeys(), err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)

				return
			}

			if exists {
				err = c.serviceExportUpdated(obj, key)
			}

			if err != nil {
				c.queue.AddRateLimited(key)
			} else {
				c.queue.Forget(key)
			}
		}()
	}
}

func (c *MCSServiceExportController) serviceExportUpdated(obj interface{}, key string) error {
	serviceExportCreated := obj.(*mcsv1a1.ServiceExport)

	objMeta := serviceExportCreated.GetObjectMeta()
	currentLHExport, err := c.lighthouseClient.LighthouseV2alpha1().ServiceExports(objMeta.GetNamespace()).
		Get(objMeta.GetName(), metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	lhExportStatus := createServiceExportStatus(serviceExportCreated)
	if lhExportStatus == nil {
		return nil
	}

	currentLHExport.Status = *lhExportStatus

	_, err = c.lighthouseClient.LighthouseV2alpha1().ServiceExports(objMeta.GetNamespace()).Update(currentLHExport)

	if err != nil {
		return err
	}

	return nil
}

func createServiceExportStatus(export *mcsv1a1.ServiceExport) *lighthousev2a1.ServiceExportStatus {
	if len(export.Status.Conditions) == 0 {
		return nil
	}
	var lhConditionsList []lighthousev2a1.ServiceExportCondition

	for _, status := range export.Status.Conditions {
		if status.Type == mcsv1a1.ServiceExportValid {
			lhStatus := lighthousev2a1.ServiceExportCondition{
				Type:               lighthousev2a1.ServiceExportExported,
				Status:             status.Status,
				LastTransitionTime: status.LastTransitionTime,
				Reason:             status.Reason,
				Message:            status.Message,
			}
			lhConditionsList = append(lhConditionsList, lhStatus)
		}
	}

	if len(lhConditionsList) != 0 {
		return &lighthousev2a1.ServiceExportStatus{
			Conditions: lhConditionsList,
		}
	}

	return nil
}
