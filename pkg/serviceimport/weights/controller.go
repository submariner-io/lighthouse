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
package weights

import (
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	lighthousev1a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1alpha1"
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
	NewClientset    func(kubeConfig *rest.Config) (kubernetes.Interface, error)
	weightsInformer cache.Controller
	weightsStore    cache.Store
	stopCh          chan struct{}
	localClusterID  string
}

func NewController(localClusterID string) *Controller {
	return &Controller{
		NewClientset: func(c *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(c) // nolint:wrapcheck // Let the caller wrap it.
		},
		stopCh:         make(chan struct{}),
		localClusterID: localClusterID,
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting Services Import Weights Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return errors.Wrap(err, "error creating client set")
	}

	// nolint:wrapcheck // Let the caller wrap these errors.
	c.weightsStore, c.weightsInformer = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientSet.LighthouseV1alpha1().ServiceImportWeights(metav1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientSet.LighthouseV1alpha1().ServiceImportWeights(metav1.NamespaceAll).Watch(context.TODO(), options)
			},
		},
		&lighthousev1a1.ServiceImportWeights{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    func(_ interface{}) {},
			UpdateFunc: func(_, _ interface{}) {},
			DeleteFunc: func(_ interface{}) {},
		},
	)

	go c.weightsInformer.Run(c.stopCh)

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)

	klog.Infof("Services Controller stopped")
}

func (c *Controller) GetWeightFor(service, namesapce, inCluster string) int64 {
	obj, exists, err := c.weightsStore.GetByKey("submariner")
	if err != nil {
		klog.V(log.DEBUG).Infof("Error trying to get weight map for local cluster %q", c.localClusterID)
		return 1.0
	}

	if !exists {
		return 1.0
	}
	if clusterWeightMap, ok := obj.(*lighthousev1a1.ServiceImportWeights).Spec.SourceClusterWeighsMap[c.localClusterID]; ok {
		if namespaceWeightMap, ok := clusterWeightMap.NamespaceWeightMap[namesapce]; ok {
			if serviceWeightMap, ok := namespaceWeightMap.ServiceWeightMap[service]; ok {
				if targetClusterWeight, ok := serviceWeightMap[inCluster]; ok {
					return targetClusterWeight
				}
			}
		}
	}

	return 1.0
}
