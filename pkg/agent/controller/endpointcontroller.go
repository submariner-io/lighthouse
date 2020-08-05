package controller

import (
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

// The ServiceImportController listens for a service import created in the submariner-operator namespace
// and creates an EndpointController in response. The EndpointController will use the app label as filter
// to listen only for the endpoints event related to ServiceImport created
type ServiceImportController struct {
	kubeClientSet       kubernetes.Interface
	lighthouseClient    lighthouseClientset.Interface
	serviceInformer     cache.SharedIndexInformer
	queue               workqueue.RateLimitingInterface
	endpointController  map[string]EndpointController
	clusterID           string
	lighthouseNamespace string
}

// Each EndpointController listens for the endpoints that backs a service and have a ServiceImport
// It will create an endpoint slice corresponding to an endpoint object and set the owner references
// to ServiceImport. The app label from the endpoint will be added to endpoint slice as well.
type EndpointController struct {
	kubeClientSet     kubernetes.Interface
	endpointInformer  cache.SharedIndexInformer
	endPointqueue     workqueue.RateLimitingInterface
	serviceImportuid  types.UID
	clusterID         string
	serviceImportName string
	stopCh            chan struct{}
}

func NewController(spec *AgentSpecification, kubeClientSet kubernetes.Interface,
	lighthouseClient lighthouseClientset.Interface) (*ServiceImportController, error) {
	serviceImportController := ServiceImportController{
		queue:               workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		kubeClientSet:       kubeClientSet,
		lighthouseClient:    lighthouseClient,
		endpointController:  make(map[string]EndpointController),
		clusterID:           spec.ClusterID,
		lighthouseNamespace: spec.Namespace,
	}

	return &serviceImportController, nil
}

func (c *ServiceImportController) Start(stopCh <-chan struct{}) error {
	c.serviceInformer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return c.lighthouseClient.LighthouseV2alpha1().ServiceImports(c.lighthouseNamespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return c.lighthouseClient.LighthouseV2alpha1().ServiceImports(c.lighthouseNamespace).Watch(options)
			},
		},
		&lighthousev2a1.ServiceImport{},
		0,
		cache.Indexers{},
	)

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
				c.serviceImportDeleted(obj, key)
			}
		},
	})

	go c.serviceInformer.Run(stopCh)
	go c.runServiceImportWorker()

	go c.Stop(stopCh)

	return nil
}

func (c *ServiceImportController) Stop(stopCh <-chan struct{}) {
	<-stopCh
	c.queue.ShutDown()

	klog.Infof("ServiceImport Controller stopped")
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
				c.serviceImportCreatedOrUpdated(obj, key)
			} else {
				klog.Errorf("Does not exist with key %q from the cache %v: %v", key, c.serviceInformer.GetIndexer().ListKeys(), err)
			}

			c.queue.Forget(key)
		}()
	}
}

func (c *ServiceImportController) serviceImportCreatedOrUpdated(obj interface{}, key string) {
	if _, found := c.endpointController[key]; found {
		klog.V(2).Infof("The endpointController is already running for  key %q", key)
		return
	}

	serviceImportCreated := obj.(*lighthousev2a1.ServiceImport)
	annotations := serviceImportCreated.ObjectMeta.Annotations
	serviceNameSpace := annotations[OriginNamespace]
	serviceName := annotations[OriginName]

	service, err := c.kubeClientSet.CoreV1().Services(serviceNameSpace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error retrieving the service  %q from the namespace %q : %v", serviceName, serviceNameSpace, err)
		return
	}

	if service.Spec.Selector == nil {
		klog.Errorf("The service %q in the namespace %q without selector is not supported", serviceName, serviceNameSpace)
		return
	}

	labelSelector := labels.Set(service.Spec.Selector).AsSelector()
	endpointInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector.String()
				return c.kubeClientSet.CoreV1().Endpoints(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector.String()
				return c.kubeClientSet.CoreV1().Endpoints(metav1.NamespaceAll).Watch(options)
			},
		},
		&corev1.Endpoints{},
		0,
		cache.Indexers{},
	)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	endpointController := EndpointController{
		endpointInformer:  endpointInformer,
		endPointqueue:     queue,
		serviceImportuid:  serviceImportCreated.ObjectMeta.UID,
		serviceImportName: serviceImportCreated.ObjectMeta.Name,
		kubeClientSet:     c.kubeClientSet,
		clusterID:         c.clusterID,
		stopCh:            make(chan struct{}),
	}

	endpointInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.Infof("Endpoint %q added", key)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.Infof("Endpoint %q updated", key)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.Infof("Endpoint %q deleted", key)
			if err == nil {
				endpointController.endPointDeleted(obj, key)
			}
		},
	})

	go endpointInformer.Run(endpointController.stopCh)
	go endpointController.runEndpointWorker(endpointInformer, queue)
	c.endpointController[key] = endpointController
}

func (c *ServiceImportController) serviceImportDeleted(obj interface{}, key string) {
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

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		matchLabels := si.ObjectMeta.Labels
		labelSelector := labels.Set(map[string]string{"app": matchLabels["app"]}).AsSelector()
		err := c.kubeClientSet.DiscoveryV1beta1().EndpointSlices(si.Namespace).
			DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector.String()})
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("Deleting EndpointSlice for Service Import %v from NameSpace %s failed due to %v", si, si.Namespace, err)
			return err
		}
		c.endpointController[key].endPointqueue.ShutDown()
		close(c.endpointController[key].stopCh)
		delete(c.endpointController, key)
		return nil
	})
	if retryErr != nil {
		klog.Errorf("Deleting EndpointSlice for Service Import %s from NameSpace %s failed after retry %v",
			si.Name, si.Namespace, retryErr)
	}
}

func (e *EndpointController) runEndpointWorker(informer cache.SharedIndexInformer, queue workqueue.RateLimitingInterface) {
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
				klog.Errorf("Error retrieving the object with store is  %v from the cache: %v", informer.GetIndexer().ListKeys(), err)
				// requeue the item to work on later
				queue.AddRateLimited(key)

				return
			}

			if exists {
				e.endPointCreatedOrUpdated(obj, key)
			} else {
				klog.Errorf("Does not exist with key %q from the cache %v: %v", key, informer.GetIndexer().ListKeys(), err)
			}

			queue.Forget(key)
		}()
	}
}

func (e *EndpointController) endPointCreatedOrUpdated(obj interface{}, key string) {
	endPoints := obj.(*corev1.Endpoints)
	endpointSliceName := endPoints.Name + "-" + e.clusterID
	newEndPointSlice := e.endpointSliceFromEndpoints(endPoints)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentEndpointSice, err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).
			Get(endpointSliceName, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("Retrieving EndpointSlice %s from NameSpace %s failed due to %v", endpointSliceName, endPoints.Namespace, err)
			return err
		}
		if currentEndpointSice != nil && currentEndpointSice.ObjectMeta.Name != "" {
			if equality.Semantic.DeepEqual(currentEndpointSice, newEndPointSlice) {
				return nil
			}
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).Update(newEndPointSlice)
		} else {
			_, err = e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).Create(newEndPointSlice)
		}
		if err != nil {
			klog.Errorf("Creating EndpointSlice %s from NameSpace %s failed due to : %v", endpointSliceName, endPoints.Namespace, err)
			return err
		}
		return nil
	})
	if retryErr != nil {
		klog.Errorf("Creating EndpointSlice %s from NameSpace %s failed after retry %v", endpointSliceName, endPoints.Namespace,
			retryErr)
	}
}

func (e *EndpointController) endPointDeleted(obj interface{}, key string) {
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

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		matchLabels := endPoints.ObjectMeta.Labels
		labelSelector := labels.Set(map[string]string{"app": matchLabels["app"]}).AsSelector()
		err := e.kubeClientSet.DiscoveryV1beta1().EndpointSlices(endPoints.Namespace).
			DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector.String()})
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("Deleting EndpointSlice for endPoints %s from NameSpace %s failed due to %v", endPoints, endPoints.Namespace, err)
			return err
		}
		return nil
	})
	if retryErr != nil {
		klog.Errorf("Deleting EndpointSlice for endPoints %s from NameSpace %s failed after retry %v",
			endPoints.Name, endPoints.Namespace, retryErr)
	}
}

func (e *EndpointController) endpointSliceFromEndpoints(endpoints *corev1.Endpoints) *discovery.EndpointSlice {
	endpointSlice := &discovery.EndpointSlice{}
	controllerFlag := false
	endpointSlice.Name = endpoints.Name + "-" + e.clusterID
	endpointLabels := endpoints.ObjectMeta.Labels
	endpointSlice.Labels = map[string]string{discovery.LabelServiceName: endpoints.Name,
		"app": endpointLabels["app"]}
	endpointSlice.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         "lighthouse.submariner.io.v2alpha1",
		Kind:               "ServiceImport",
		Name:               e.serviceImportName,
		UID:                e.serviceImportuid,
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
		TargetRef:  address.TargetRef,
		Topology:   topology,
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
