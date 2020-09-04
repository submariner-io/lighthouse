package endpointslice

import (
	"fmt"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

type Controller struct {
	// Indirection hook for unit tests to supply fake client sets
	NewClientset func(kubeConfig *rest.Config) (kubernetes.Interface, error)
	epsInformer  cache.Controller
	stopCh       chan struct{}
	mapStore     Store
	store        cache.Store
}

func NewController(endpointSliceStore Store) *Controller {
	return &Controller{
		NewClientset: func(c *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(c)
		},
		stopCh:   make(chan struct{}),
		mapStore: endpointSliceStore,
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting EndpointSlice Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("Error creating client set: %v", err)
	}

	labelMap := map[string]string{
		discovery.LabelManagedBy: lhconstants.LabelValueManagedBy,
	}
	labelSelector := labels.Set(labelMap).String()

	c.store, c.epsInformer = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector
				return clientSet.DiscoveryV1beta1().EndpointSlices(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector
				return clientSet.DiscoveryV1beta1().EndpointSlices(metav1.NamespaceAll).Watch(options)
			},
		},
		&discovery.EndpointSlice{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err == nil {
					c.endpointSliceCreatedOrUpdated(key)
				}
			},
			UpdateFunc: func(obj interface{}, new interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err == nil {
					c.endpointSliceCreatedOrUpdated(key)
				}
			},
			DeleteFunc: func(obj interface{}) {
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				if err == nil {
					var endpointSlice *discovery.EndpointSlice
					var ok bool
					if endpointSlice, ok = obj.(*discovery.EndpointSlice); !ok {
						tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
						if !ok {
							klog.Errorf("Failed to get deleted endpointSlice object for key %s", key)
							return
						}

						endpointSlice, ok = tombstone.Obj.(*discovery.EndpointSlice)

						if !ok {
							klog.Errorf("Failed to convert deleted tombstone object %v  to endpointSlice", tombstone.Obj)
							return
						}
					}
					c.mapStore.Remove(endpointSlice)
				}
			},
		},
	)

	go c.epsInformer.Run(c.stopCh)

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)

	klog.Infof("EndpointSlice Controller stopped")
}

func (c *Controller) endpointSliceCreatedOrUpdated(key string) {
	obj, exists, err := c.store.GetByKey(key)
	if err != nil {
		klog.Errorf("Error retrieving the object with key  %s from the cache: %v", key, err)
		return
	}

	if exists {
		endpointSlice := obj.(*discovery.EndpointSlice)
		c.mapStore.Put(endpointSlice)
	}
}
