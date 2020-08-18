package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
)

func NewEndpointSliceController(spec *AgentSpecification, cfg *rest.Config) (*EndpointSliceController, error) {
	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error building clientset: %s", err.Error())
	}

	endpointSliceController := EndpointSliceController{
		endPointSlicequeue: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		kubeClientSet:      kubeClientSet,
		clusterID:          spec.ClusterID,
		namespace:          spec.Namespace,
	}

	return &endpointSliceController, nil
}

func (e *EndpointSliceController) Start(stopCh <-chan struct{}) error {
	e.endpointSliceInformer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(e.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(e.namespace).Watch(options)
			},
		},
		&discovery.EndpointSlice{},
		0,
		cache.Indexers{},
	)

	e.endpointSliceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.Infof("EndpointSlice %q added", key)
			if err == nil {
				e.endPointSlicequeue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.Infof("EndpointSlice %q updated", key)
			if err == nil {
				e.endPointSlicequeue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.Infof("EndpointSlice %q deleted", key)
			if err == nil {
				var ok bool
				var endPointSlice *discovery.EndpointSlice
				if endPointSlice, ok = obj.(*discovery.EndpointSlice); !ok {
					tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
					if !ok {
						klog.Errorf("Failed to get deleted EndpointSlice object for key %s, serviceImport %v", key, endPointSlice)
						return
					}

					endPointSlice, ok = tombstone.Obj.(*discovery.EndpointSlice)

					if !ok {
						klog.Errorf("Failed to convert deleted tombstone object %v  to endPointslice", tombstone.Obj)
						return
					}
				}
				e.endPointSlicequeue.Add(key)
				e.endpointSliceDeletedMap.Store(endPointSlice, key)
			}
		},
	})

	go e.endpointSliceInformer.Run(e.stopCh)
	go e.runEndpointSliceWorker(e.endpointSliceInformer, e.endPointSlicequeue)

	go func(stopCh <-chan struct{}) {
		<-stopCh
		e.endPointSlicequeue.ShutDown()

		klog.Infof("ServiceImport Controller stopped")
	}(stopCh)

	return nil
}

func (e *EndpointSliceController) runEndpointSliceWorker(informer cache.SharedIndexInformer, queue workqueue.RateLimitingInterface) {
	for {
		keyObj, shutdown := queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for ServiceImports stopped")
			return
		}

		key := keyObj.(string)

		func() {
			defer queue.Done(key)
			obj, exists, err := informer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving the object with key  %s from the cache: %v", key, err)
				// requeue the item to work on later
				queue.AddRateLimited(key)

				return
			}

			if exists {
				err = e.endPointSliceCreatedOrUpdated(obj, key)
			} else {
				err = e.endPointSliceDeleted(key)
			}

			if err != nil {
				if !exists {
					e.endpointSliceDeletedMap.Store(key, obj)
				}

				queue.AddRateLimited(key)
			} else {
				queue.Forget(key)
			}
		}()
	}
}

func (e *EndpointSliceController) endPointSliceCreatedOrUpdated(obj interface{}, key string) error {
	endPointSlice := obj.(*discovery.EndpointSlice)
	labels := endPointSlice.GetObjectMeta().GetLabels()

	if labels[labelSourceCluster] == e.clusterID {
		klog.Infof("SOurce Label %s is same as %s", labels[labelSourceCluster], e.clusterID)
		return nil
	}

	newEndPointSlice := newEndPointSlice(endPointSlice, labels[labelSourceNamespace])
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentEndpointSice, err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(labels[labelSourceNamespace]).
			Get(newEndPointSlice.Name, metav1.GetOptions{})

		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		if errors.IsNotFound(err) {
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(labels[labelSourceNamespace]).Create(newEndPointSlice)
		} else {
			klog.Infof("Checking equivalencey")
			if endpointSliceEquivalent(currentEndpointSice, newEndPointSlice) {
				return nil
			}
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(labels[labelSourceNamespace]).Update(newEndPointSlice)
		}
		if err != nil {
			return err
		}
		return nil
	})
	if retryErr != nil {
		klog.Errorf("Creating EndpointSlice %s from NameSpace %s failed after retry %v", newEndPointSlice.Name,
			labels[labelSourceNamespace],
			retryErr)
	}

	return retryErr
}

func (e *EndpointSliceController) endPointSliceDeleted(key string) error {
	obj, found := e.endpointSliceDeletedMap.Load(key)
	if !found {
		klog.Errorf("deleting endpointSlice for key %s failed since object not found", key)
		return nil
	}

	e.endpointSliceDeletedMap.Delete(key)

	endPointSlice := obj.(discovery.EndpointSlice)
	err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPointSlice.Namespace).
		Delete(endPointSlice.Name, &metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("Deleting EndpointSlice for endPoints %v from NameSpace %s failed due to %v", endPointSlice, endPointSlice.Namespace, err)
		return err
	}

	return nil
}

func newEndPointSlice(endpointSlice *discovery.EndpointSlice, namespace string) *discovery.EndpointSlice {
	return &discovery.EndpointSlice{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpointSlice.Name,
			Namespace: namespace,
			Labels:    endpointSlice.GetLabels(),
		},
		AddressType: endpointSlice.AddressType,
		Endpoints:   endpointSlice.Endpoints,
		Ports:       endpointSlice.Ports,
	}
}
