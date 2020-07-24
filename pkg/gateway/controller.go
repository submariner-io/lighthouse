package gateway

import (
	"fmt"
	"sync/atomic"

	"github.com/submariner-io/admiral/pkg/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type NewClientsetFunc func(c *rest.Config) (dynamic.Interface, error)

// Indirection hook for unit tests to supply fake client sets
var NewClientset NewClientsetFunc

type Controller struct {
	newClientset     NewClientsetFunc
	informer         cache.Controller
	store            cache.Store
	queue            workqueue.RateLimitingInterface
	stopCh           chan struct{}
	clusterStatusMap atomic.Value
	gatewayAvailable bool
}

func NewController() *Controller {
	controller := &Controller{
		newClientset:     getNewClientsetFunc(),
		queue:            workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		stopCh:           make(chan struct{}),
		gatewayAvailable: true,
	}
	controller.clusterStatusMap.Store(make(map[string]bool))

	return controller
}

func getNewClientsetFunc() NewClientsetFunc {
	if NewClientset != nil {
		return NewClientset
	}

	return dynamic.NewForConfig
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	gwClientset, err := c.getCheckedClientset(kubeConfig)
	if errors.IsNotFound(err) {
		klog.Infof("Gateway resource not found, disabling Gateway status controller")

		c.gatewayAvailable = false

		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Starting Gateway status Controller")

	c.store, c.informer = cache.NewInformer(&cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return gwClientset.List(metav1.ListOptions{})
		},
		WatchFunc: gwClientset.Watch,
	}, &unstructured.Unstructured{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.V(log.DEBUG).Infof("GatewayStatus %q updated", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.V(log.DEBUG).Infof("GatewayStatus %q deleted", key)
		},
	})

	go c.informer.Run(c.stopCh)
	go c.runWorker()

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)
	c.queue.ShutDown()
	klog.Infof("Gateway status Controller stopped")
}

func (c *Controller) runWorker() {
	for {
		keyObj, shutdown := c.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for Gateways stopped")
			return
		}

		key := keyObj.(string)

		func() {
			defer c.queue.Done(key)
			obj, exists, err := c.store.GetByKey(key)
			if err != nil {
				klog.Errorf("Error retrieving gateway with key %q from the cache: %v", key, err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)

				return
			}

			if exists {
				c.gatewayCreatedOrUpdated(obj.(*unstructured.Unstructured))
			}

			c.queue.Forget(key)
		}()
	}
}

func (c *Controller) gatewayCreatedOrUpdated(obj *unstructured.Unstructured) {
	haStatus, connections, ok := getGatewayStatus(obj)
	if !ok || haStatus != "active" {
		return
	}
	var newMap map[string]bool

	currentMap := c.getClusterStatusMap()

	for _, connection := range connections {
		connectionMap := connection.(map[string]interface{})

		status, found, err := unstructured.NestedString(connectionMap, "status")
		if err != nil || !found {
			klog.Errorf("status field not found in %#v", connectionMap)
		}

		clusterId, found, err := unstructured.NestedString(connectionMap, "endpoint", "cluster_id")
		if !found || err != nil {
			klog.Errorf("clusterId field not found in %#v", connectionMap)
			return
		}

		if status == "connected" {
			_, found := currentMap[clusterId]
			if !found {
				if newMap == nil {
					newMap = copyMap(currentMap)
				}

				newMap[clusterId] = true
			}
		} else {
			_, found = currentMap[clusterId]
			if found {
				if newMap == nil {
					newMap = copyMap(currentMap)
				}
				delete(newMap, clusterId)
			}
		}
	}

	if newMap != nil {
		klog.Infof("Updating the gateway status %#v ", newMap)
		c.clusterStatusMap.Store(newMap)
	}
}

func getGatewayStatus(obj *unstructured.Unstructured) (haStatus string, connections []interface{}, gwStatus bool) {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if !found || err != nil {
		klog.Errorf("status field not found in %#v, err was: %v", obj, err)
		return "", nil, false
	}

	haStatus, found, err = unstructured.NestedString(status, "haStatus")
	if !found || err != nil {
		klog.Errorf("haStatus field not found in %#v, err was: %v", status, err)
		return "", nil, false
	}

	connections, found, err = unstructured.NestedSlice(status, "connections")
	if !found || err != nil {
		klog.Errorf("connections field not found in %#v, err was: %v", status, err)
		return haStatus, nil, false
	}

	return haStatus, connections, true
}

func (c *Controller) getClusterStatusMap() map[string]bool {
	return c.clusterStatusMap.Load().(map[string]bool)
}

func (c *Controller) IsConnected(clusterId string) bool {
	return !c.gatewayAvailable || c.getClusterStatusMap()[clusterId]
}

func (c *Controller) getCheckedClientset(kubeConfig *rest.Config) (dynamic.ResourceInterface, error) {
	clientSet, err := c.newClientset(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating client set: %v", err)
	}

	gvr, _ := schema.ParseResourceArg("gateways.v1.submariner.io")
	gwClient := clientSet.Resource(*gvr).Namespace(v1.NamespaceAll)
	_, err = gwClient.List(metav1.ListOptions{})

	return gwClient, err
}

func copyMap(src map[string]bool) map[string]bool {
	m := make(map[string]bool)
	for k, v := range src {
		m[k] = v
	}

	return m
}
