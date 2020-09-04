package controller

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	utilnet "k8s.io/utils/net"
)

func NewEndpointController(kubeClientSet kubernetes.Interface, serviceImportuid types.UID, serviceImportName,
	serviceImportNameSpace, clusterId string) *EndpointController {
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	endpointController := &EndpointController{
		endPointqueue:                queue,
		serviceImportUID:             serviceImportuid,
		serviceImportName:            serviceImportName,
		serviceImportSourceNameSpace: serviceImportNameSpace,
		kubeClientSet:                kubeClientSet,
		clusterID:                    clusterId,
		stopCh:                       make(chan struct{}),
	}

	return endpointController
}

func (e *EndpointController) Start(stopCh <-chan struct{}, labelSelector fmt.Stringer) {
	e.store, e.endpointInformer = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector.String()
				return e.kubeClientSet.CoreV1().Endpoints(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector.String()
				return e.kubeClientSet.CoreV1().Endpoints(metav1.NamespaceAll).Watch(options)
			},
		},
		&corev1.Endpoints{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				klog.V(log.DEBUG).Infof("Endpoint %q added", key)
				if err == nil {
					e.endPointqueue.Add(key)
				}
			},
			UpdateFunc: func(obj interface{}, new interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(new)
				klog.V(log.DEBUG).Infof("Endpoint %q updated", key)
				if err == nil {
					e.endPointqueue.Add(key)
				}
			},
			DeleteFunc: func(obj interface{}) {
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				klog.V(log.DEBUG).Infof("Endpoint %q deleted", key)
				if err == nil {
					var endPoints *corev1.Endpoints
					var ok bool
					if endPoints, ok = obj.(*corev1.Endpoints); !ok {
						tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
						if !ok {
							klog.Errorf("Failed to get deleted endPoints object for key %s, serviceImport %v", key, endPoints)
							return
						}

						endPoints, ok = tombstone.Obj.(*corev1.Endpoints)

						if !ok {
							klog.Errorf("Failed to convert deleted tombstone object %v  to endPoints", tombstone.Obj)
							return
						}
					}
					e.endpointDeletedMap.Store(endPoints, key)
					e.endPointqueue.Add(key)
				}
			},
		},
	)

	go e.endpointInformer.Run(e.stopCh)
	go e.runEndpointWorker(e.endpointInformer, e.endPointqueue)
}

func (e *EndpointController) Stop() {
	e.endPointqueue.ShutDown()
	close(e.stopCh)
}

func (e *EndpointController) runEndpointWorker(informer cache.Controller, queue workqueue.RateLimitingInterface) {
	for {
		keyObj, shutdown := queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for ServiceImports stopped")
			return
		}

		key := keyObj.(string)

		func() {
			defer queue.Done(key)
			obj, exists, err := e.store.GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving the object with key  %s from the cache: %v", key, err)
				// requeue the item to work on later
				queue.AddRateLimited(key)

				return
			}

			if exists {
				err = e.endPointCreatedOrUpdated(obj, key)
			} else {
				err = e.endPointDeleted(key)
			}

			if err != nil {
				if !exists {
					e.endpointDeletedMap.Store(key, obj)
				}

				queue.AddRateLimited(key)
			} else {
				queue.Forget(key)
			}
		}()
	}
}

func (e *EndpointController) endPointCreatedOrUpdated(obj interface{}, key string) error {
	endPoints := obj.(*corev1.Endpoints)
	newEndPointSlice := e.endpointSliceFromEndpoints(endPoints)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentEndpointSice, err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).
			Get(newEndPointSlice.Name, metav1.GetOptions{})

		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		if errors.IsNotFound(err) {
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).Create(newEndPointSlice)
		} else {
			if endpointSliceEquivalent(currentEndpointSice, newEndPointSlice) {
				return nil
			}
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).Update(newEndPointSlice)
		}
		if err != nil {
			return err
		}
		return nil
	})
	if retryErr != nil {
		klog.Errorf("Creating EndpointSlice %s from NameSpace %s failed after retry %v", newEndPointSlice.Name, endPoints.Namespace,
			retryErr)
	}

	return retryErr
}

func (e *EndpointController) endPointDeleted(key string) error {
	obj, found := e.endpointDeletedMap.Load(key)
	if !found {
		klog.Errorf("deleting endpointSlice for key %s failed since object not found", key)
		return nil
	}

	e.endpointDeletedMap.Delete(key)

	endPoints := obj.(corev1.Endpoints)
	endpointSliceName := endPoints.Name + "-" + e.clusterID
	err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).
		Delete(endpointSliceName, &metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("Deleting EndpointSlice for endPoints %v from NameSpace %s failed due to %v", endPoints, endPoints.Namespace, err)
		return err
	}

	return nil
}

func (e *EndpointController) endpointSliceFromEndpoints(endpoints *corev1.Endpoints) *discovery.EndpointSlice {
	endpointSlice := &discovery.EndpointSlice{}
	controllerFlag := false
	endpointSlice.Name = endpoints.Name + "-" + e.clusterID
	endpointSlice.Labels = map[string]string{
		lhconstants.LabelServiceImportName: e.serviceImportName,
		discovery.LabelManagedBy:           lhconstants.LabelValueManagedBy,
		lhconstants.LabelSourceNamespace:   e.serviceImportSourceNameSpace,
		lhconstants.LabelSourceCluster:     e.clusterID,
	}
	endpointSlice.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         "lighthouse.submariner.io.v2alpha1",
		Kind:               "ServiceImport",
		Name:               e.serviceImportName,
		UID:                e.serviceImportUID,
		Controller:         &controllerFlag,
		BlockOwnerDeletion: nil,
	}}

	endpointSlice.AddressType = discovery.AddressTypeIPv4

	if len(endpoints.Subsets) > 0 {
		subset := endpoints.Subsets[0]
		for i := range subset.Ports {
			endpointSlice.Ports = append(endpointSlice.Ports, discovery.EndpointPort{
				Port:     &subset.Ports[i].Port,
				Name:     &subset.Ports[i].Name,
				Protocol: &subset.Ports[i].Protocol,
			})
		}

		if allAddressesIPv6(append(subset.Addresses, subset.NotReadyAddresses...)) {
			endpointSlice.AddressType = discovery.AddressTypeIPv6
		}

		endpointSlice.Endpoints = append(endpointSlice.Endpoints, getEndpointsFromAddresses(subset.Addresses, endpointSlice.AddressType, true)...)
		endpointSlice.Endpoints = append(endpointSlice.Endpoints, getEndpointsFromAddresses(subset.NotReadyAddresses,
			endpointSlice.AddressType, false)...)
	}

	return endpointSlice
}

func getEndpointsFromAddresses(addresses []corev1.EndpointAddress, addressType discovery.AddressType, ready bool) []discovery.Endpoint {
	endpoints := []discovery.Endpoint{}
	isIPv6AddressType := addressType == discovery.AddressTypeIPv6

	for _, address := range addresses {
		if utilnet.IsIPv6String(address.IP) == isIPv6AddressType {
			endpoints = append(endpoints, endpointFromAddress(address, ready))
		}
	}

	return endpoints
}

func endpointFromAddress(address corev1.EndpointAddress, ready bool) discovery.Endpoint {
	topology := map[string]string{}
	if address.NodeName != nil {
		topology["kubernetes.io/hostname"] = *address.NodeName
	}

	return discovery.Endpoint{
		Addresses:  []string{address.IP},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Topology:   topology,
		Hostname:   &address.Hostname,
	}
}

func allAddressesIPv6(addresses []corev1.EndpointAddress) bool {
	if len(addresses) == 0 {
		return false
	}

	for _, address := range addresses {
		if !utilnet.IsIPv6String(address.IP) {
			return false
		}
	}

	return true
}

func endpointSliceEquivalent(obj1, obj2 *discovery.EndpointSlice) bool {
	return equality.Semantic.DeepEqual(obj1.Endpoints, obj2.Endpoints) &&
		equality.Semantic.DeepEqual(obj1.Ports, obj2.Ports) &&
		equality.Semantic.DeepEqual(obj1.AddressType, obj2.AddressType)
}
