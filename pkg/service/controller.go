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
package service

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	svcInformer  cache.Controller
	svcStore     cache.Store
	stopCh       chan struct{}
}

func NewController() *Controller {
	return &Controller{
		NewClientset: func(c *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(c)
		},
		stopCh: make(chan struct{}),
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting Services Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("error creating client set: %v", err)
	}

	c.svcStore, c.svcInformer = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientSet.CoreV1().Services(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientSet.CoreV1().Services(metav1.NamespaceAll).Watch(options)
			},
		},
		&v1.Service{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) {},
			UpdateFunc: func(old interface{}, new interface{}) {},
			DeleteFunc: func(obj interface{}) {},
		},
	)

	go c.svcInformer.Run(c.stopCh)

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)

	klog.Infof("Services Controller stopped")
}

func (c *Controller) GetIP(name, namespace string) (string, bool) {
	key := namespace + "/" + name
	obj, exists, err := c.svcStore.GetByKey(key)

	if err != nil {
		klog.V(log.DEBUG).Infof("Error trying to get service for key %q", key)
		return "", false
	}

	if !exists {
		return "", false
	}

	svc := obj.(*v1.Service)

	if svc.Spec.Type == v1.ServiceTypeClusterIP && svc.Spec.ClusterIP != "" {
		return svc.Spec.ClusterIP, true
	}

	return "", false
}
