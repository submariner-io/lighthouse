/*
© 2020 Red Hat, Inc. and others

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
package endpointslice

import (
	"context"
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

type NewClientsetFunc func(kubeConfig *rest.Config) (kubernetes.Interface, error)

// NewClientset is an indirection hook for unit tests to supply fake client sets
var NewClientset NewClientsetFunc

type Controller struct {
	// Indirection hook for unit tests to supply fake client sets
	NewClientset NewClientsetFunc
	epsInformer  cache.Controller
	stopCh       chan struct{}
	store        Store
	clientSet    kubernetes.Interface
}

func NewController(endpointSliceStore Store) *Controller {
	return &Controller{
		NewClientset: getNewClientsetFunc(),
		stopCh:       make(chan struct{}),
		store:        endpointSliceStore,
	}
}

func getNewClientsetFunc() NewClientsetFunc {
	if NewClientset != nil {
		return NewClientset
	}

	return func(c *rest.Config) (kubernetes.Interface, error) {
		return kubernetes.NewForConfig(c)
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting EndpointSlice Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("error creating client set: %v", err)
	}

	c.clientSet = clientSet
	labelMap := map[string]string{
		discovery.LabelManagedBy: lhconstants.LabelValueManagedBy,
	}
	labelSelector := labels.Set(labelMap).String()

	_, c.epsInformer = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector
				return clientSet.DiscoveryV1beta1().EndpointSlices(metav1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector
				return clientSet.DiscoveryV1beta1().EndpointSlices(metav1.NamespaceAll).Watch(context.TODO(), options)
			},
		},
		&discovery.EndpointSlice{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				c.store.Put(obj.(*discovery.EndpointSlice))
			},
			UpdateFunc: func(old interface{}, new interface{}) {
				c.store.Put(new.(*discovery.EndpointSlice))
			},
			DeleteFunc: func(obj interface{}) {
				var endpointSlice *discovery.EndpointSlice
				var ok bool
				if endpointSlice, ok = obj.(*discovery.EndpointSlice); !ok {
					tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
					if !ok {
						klog.Errorf("Failed to get deleted endpointSlice object %v", obj)
						return
					}

					endpointSlice, ok = tombstone.Obj.(*discovery.EndpointSlice)

					if !ok {
						klog.Errorf("Failed to convert deleted tombstone object %v  to endpointSlice", tombstone.Obj)
						return
					}
				}
				c.store.Remove(endpointSlice)
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

func (c *Controller) IsHealthy(name, namespace, clusterID string) bool {
	key := keyFunc(name, namespace)
	endpointInfo := c.store.Get(key)
	if endpointInfo != nil && endpointInfo.clusterInfo != nil {
		info := endpointInfo.clusterInfo[clusterID]
		if info != nil {
			return len(info.ipList) > 0
		}
	}

	return false
}
