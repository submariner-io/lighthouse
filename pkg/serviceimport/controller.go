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
	"k8s.io/klog"
)

type NewClientsetFunc func(kubeConfig *rest.Config) (lighthouseClientset.Interface, error)

// Indirection hook for unit tests to supply fake client sets
var NewClientset NewClientsetFunc

type Controller struct {
	// Indirection hook for unit tests to supply fake client sets
	NewClientset    NewClientsetFunc
	serviceInformer cache.SharedIndexInformer
	stopCh          chan struct{}
	store           Store
}

func NewController(serviceImportStore Store) *Controller {
	return &Controller{
		NewClientset: getNewClientsetFunc(),
		store:        serviceImportStore,
		stopCh:       make(chan struct{}),
	}
}

func getNewClientsetFunc() NewClientsetFunc {
	if NewClientset != nil {
		return NewClientset
	}

	return func(c *rest.Config) (lighthouseClientset.Interface, error) {
		return lighthouseClientset.NewForConfig(c)
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting ServiceImport Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("Error creating client set: %v", err)
	}

	informerFactory := lighthouseInformers.NewSharedInformerFactoryWithOptions(clientSet, 0,
		lighthouseInformers.WithNamespace(metav1.NamespaceAll))

	c.serviceInformer = informerFactory.Lighthouse().V2alpha1().ServiceImports().Informer()
	c.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.serviceImportCreatedOrUpdated,
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.serviceImportCreatedOrUpdated(newObj)
		},
		DeleteFunc: c.serviceImportDeleted,
	})

	go c.serviceInformer.Run(c.stopCh)

	if ok := cache.WaitForCacheSync(c.stopCh, c.serviceInformer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for informer cache to sync")
	}

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)

	klog.Infof("ServiceImport Controller stopped")
}

func (c *Controller) serviceImportCreatedOrUpdated(obj interface{}) {
	klog.V(log.DEBUG).Infof("In serviceImportCreatedOrUpdated for: %#v, ", obj)

	c.store.Put(obj.(*lighthousev2a1.ServiceImport))
}

func (c *Controller) serviceImportDeleted(obj interface{}) {
	klog.V(log.DEBUG).Infof("In serviceImportDeleted for: %#v, ", obj)

	var si *lighthousev2a1.ServiceImport
	var ok bool
	if si, ok = obj.(*lighthousev2a1.ServiceImport); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %#v to DeletedFinalStateUnknown", obj)
			return
		}

		si, ok = tombstone.Obj.(*lighthousev2a1.ServiceImport)
		if !ok {
			klog.Errorf("Could not convert object tombstone %#v to Unstructured", tombstone.Obj)
			return
		}
	}

	c.store.Remove(si)
}
