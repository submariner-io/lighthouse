package gateway

import (
	"fmt"
	"sync/atomic"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/workqueue"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

type NewClientsetFunc func(c *rest.Config) (dynamic.Interface, error)

// Indirection hook for unit tests to supply fake client sets
var NewClientset NewClientsetFunc

type Controller struct {
	NewClientset     NewClientsetFunc
	informer         cache.Controller
	store            cache.Store
	queue            workqueue.Interface
	stopCh           chan struct{}
	clusterStatusMap atomic.Value
	gatewayAvailable bool
}

func NewController() *Controller {
	controller := &Controller{
		NewClientset:     getNewClientsetFunc(),
		queue:            workqueue.New("Gateway Controller"),
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
		AddFunc: c.queue.Enqueue,
		UpdateFunc: func(old interface{}, new interface{}) {
			c.queue.Enqueue(new)
		},
		DeleteFunc: func(obj interface{}) {
			key, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.V(log.DEBUG).Infof("GatewayStatus %q deleted", key)
		},
	})

	go c.informer.Run(c.stopCh)

	if ok := cache.WaitForCacheSync(c.stopCh, c.informer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for informer cache to sync")
	}

	go c.queue.Run(c.stopCh, c.processNextGateway)

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)
	c.queue.ShutDown()
	klog.Infof("Gateway status Controller stopped")
}

func (c *Controller) processNextGateway(key, name, ns string) (bool, error) {
	obj, exists, err := c.store.GetByKey(key)
	if err != nil {
		// requeue the item to work on later
		return true, fmt.Errorf("error retrieving Gateway with key %q from the cache: %v", key, err)
	}

	if exists {
		c.gatewayCreatedOrUpdated(obj.(*unstructured.Unstructured))
	}

	return false, nil
}

func (c *Controller) gatewayCreatedOrUpdated(obj *unstructured.Unstructured) {
	connections, ok := getGatewayStatus(obj)
	if !ok {
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
			klog.Errorf("cluster_id field not found in %#v", connectionMap)
			continue
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

func getGatewayStatus(obj *unstructured.Unstructured) (connections []interface{}, gwStatus bool) {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if !found || err != nil {
		klog.Errorf("status field not found in %#v, err was: %v", obj, err)
		return nil, false
	}

	localClusterId, found, err := unstructured.NestedString(status, "localEndpoint", "cluster_id")

	if !found || err != nil {
		klog.Errorf("localEndpoint->cluster_id not found in %#v, err was: %v", status, err)
	} else {
		connections = append(connections, map[string]interface{}{
			"status": "connected",
			"endpoint": map[string]interface{}{
				"cluster_id": localClusterId,
			},
		})
	}

	haStatus, found, err := unstructured.NestedString(status, "haStatus")

	if !found || err != nil {
		klog.Errorf("haStatus field not found in %#v, err was: %v", status, err)
		return connections, true
	}

	if haStatus == "active" {
		rconns, _, err := unstructured.NestedSlice(status, "connections")
		if err != nil {
			klog.Errorf("connections field not found in %#v, err was: %v", status, err)
			return connections, false
		}

		connections = append(connections, rconns...)
	}

	return connections, true
}

func (c *Controller) getClusterStatusMap() map[string]bool {
	return c.clusterStatusMap.Load().(map[string]bool)
}

func (c *Controller) IsConnected(clusterId string) bool {
	return !c.gatewayAvailable || c.getClusterStatusMap()[clusterId]
}

func (c *Controller) getCheckedClientset(kubeConfig *rest.Config) (dynamic.ResourceInterface, error) {
	clientSet, err := c.NewClientset(kubeConfig)
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
