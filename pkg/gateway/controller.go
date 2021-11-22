/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package gateway

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/workqueue"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

type NewClientsetFunc func(c *rest.Config) (dynamic.Interface, error)

// NewClientset is an indirection hook for unit tests to supply fake client sets.
var NewClientset NewClientsetFunc

type Controller struct {
	NewClientset     NewClientsetFunc
	informer         cache.Controller
	store            cache.Store
	queue            workqueue.Interface
	stopCh           chan struct{}
	clusterStatusMap atomic.Value
	localClusterID   atomic.Value
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

	localClusterID := os.Getenv("SUBMARINER_CLUSTERID")

	klog.Infof("Setting localClusterID from env: %q", localClusterID)
	controller.localClusterID.Store(localClusterID)

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
	if apierrors.IsNotFound(err) {
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
			return gwClientset.List(context.TODO(), metav1.ListOptions{})
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return gwClientset.Watch(context.TODO(), options)
		},
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
		// requeue the item to work on later.
		return true, errors.Wrapf(err, "error retrieving Gateway with key %q from the cache", key)
	}

	if exists {
		c.gatewayCreatedOrUpdated(obj.(*unstructured.Unstructured))
	}

	return false, nil
}

func (c *Controller) gatewayCreatedOrUpdated(obj *unstructured.Unstructured) {
	connections, localClusterID, ok := getGatewayStatus(obj)
	if !ok {
		return
	}

	// Updating
	c.updateLocalClusterIDIfNeeded(localClusterID)

	c.updateClusterStatusMap(connections)
}

func (c *Controller) updateClusterStatusMap(connections []interface{}) {
	var newMap map[string]bool

	currentMap := c.getClusterStatusMap()

	for _, connection := range connections {
		connectionMap := connection.(map[string]interface{})

		status, found, err := unstructured.NestedString(connectionMap, "status")
		if err != nil || !found {
			klog.Errorf("status field not found in %#v", connectionMap)
		}

		clusterID, found, err := unstructured.NestedString(connectionMap, "endpoint", "cluster_id")
		if !found || err != nil {
			klog.Errorf("cluster_id field not found in %#v", connectionMap)
			continue
		}

		if status == "connected" {
			_, found := currentMap[clusterID]
			if !found {
				if newMap == nil {
					newMap = copyMap(currentMap)
				}

				newMap[clusterID] = true
			}
		} else {
			_, found = currentMap[clusterID]
			if found {
				if newMap == nil {
					newMap = copyMap(currentMap)
				}
				delete(newMap, clusterID)
			}
		}
	}

	if newMap != nil {
		klog.Infof("Updating the gateway status %#v ", newMap)
		c.clusterStatusMap.Store(newMap)
	}
}

func (c *Controller) updateLocalClusterIDIfNeeded(clusterID string) {
	updateNeeded := clusterID != "" && clusterID != c.LocalClusterID()
	if updateNeeded {
		klog.Infof("Updating the gateway localClusterID %q ", clusterID)
		c.localClusterID.Store(clusterID)
	}
}

func getGatewayStatus(obj *unstructured.Unstructured) (connections []interface{}, clusterID string, gwStatus bool) {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if !found || err != nil {
		klog.Errorf("status field not found in %#v, err was: %v", obj, err)
		return nil, "", false
	}

	localClusterID, found, err := unstructured.NestedString(status, "localEndpoint", "cluster_id")

	if !found || err != nil {
		klog.Errorf("localEndpoint->cluster_id not found in %#v, err was: %v", status, err)

		localClusterID = ""
	} else {
		connections = append(connections, map[string]interface{}{
			"status": "connected",
			"endpoint": map[string]interface{}{
				"cluster_id": localClusterID,
			},
		})
	}

	haStatus, found, err := unstructured.NestedString(status, "haStatus")

	if !found || err != nil {
		klog.Errorf("haStatus field not found in %#v, err was: %v", status, err)
		return connections, localClusterID, true
	}

	if haStatus == "active" {
		rconns, _, err := unstructured.NestedSlice(status, "connections")
		if err != nil {
			klog.Errorf("connections field not found in %#v, err was: %v", status, err)
			return connections, localClusterID, false
		}

		connections = append(connections, rconns...)
	}

	return connections, localClusterID, true
}

func (c *Controller) getClusterStatusMap() map[string]bool {
	return c.clusterStatusMap.Load().(map[string]bool)
}

func (c *Controller) getCheckedClientset(kubeConfig *rest.Config) (dynamic.ResourceInterface, error) {
	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating client set")
	}

	gvr, _ := schema.ParseResourceArg("gateways.v1.submariner.io")
	gwClient := clientSet.Resource(*gvr).Namespace(v1.NamespaceAll)
	_, err = gwClient.List(context.TODO(), metav1.ListOptions{})

	return gwClient, err
}

func copyMap(src map[string]bool) map[string]bool {
	m := make(map[string]bool)
	for k, v := range src {
		m[k] = v
	}

	return m
}

// Public API.
func (c *Controller) IsConnected(clusterID string) bool {
	return !c.gatewayAvailable || c.getClusterStatusMap()[clusterID]
}

func (c *Controller) LocalClusterID() string {
	return c.localClusterID.Load().(string)
}
