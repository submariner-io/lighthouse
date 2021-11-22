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
package serviceimport

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsClientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"
)

type NewClientsetFunc func(kubeConfig *rest.Config) (mcsClientset.Interface, error)

// NewClientset is an indirection hook for unit tests to supply fake client sets.
var NewClientset NewClientsetFunc

type Controller struct {
	// Indirection hook for unit tests to supply fake client sets.
	NewClientset    NewClientsetFunc
	serviceInformer cache.SharedIndexInformer
	stopCh          chan struct{}
	store           Store
}

func NewController(serviceImportStore Store) *Controller {
	return &Controller{
		NewClientset: getNewClientsetFunc(),
		store:        serviceImportStore,
		stopCh:       make(chan struct{}),
	}
}

func getNewClientsetFunc() NewClientsetFunc {
	if NewClientset != nil {
		return NewClientset
	}

	return func(c *rest.Config) (mcsClientset.Interface, error) {
		return mcsClientset.NewForConfig(c)
	}
}

func (c *Controller) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting ServiceImport Controller")

	clientSet, err := c.NewClientset(kubeConfig)
	if err != nil {
		return errors.Wrap(err, "error creating client set")
	}

	informerFactory := mcsInformers.NewSharedInformerFactoryWithOptions(clientSet, 0,
		mcsInformers.WithNamespace(metav1.NamespaceAll))

	c.serviceInformer = informerFactory.Multicluster().V1alpha1().ServiceImports().Informer()
	c.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.serviceImportCreatedOrUpdated,
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.serviceImportCreatedOrUpdated(newObj)
		},
		DeleteFunc: c.serviceImportDeleted,
	})

	go c.serviceInformer.Run(c.stopCh)

	if ok := cache.WaitForCacheSync(c.stopCh, c.serviceInformer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for informer cache to sync")
	}

	return nil
}

func (c *Controller) Stop() {
	close(c.stopCh)

	klog.Infof("ServiceImport Controller stopped")
}

func (c *Controller) serviceImportCreatedOrUpdated(obj interface{}) {
	klog.V(log.DEBUG).Infof("In serviceImportCreatedOrUpdated for: %#v, ", obj)

	c.store.Put(obj.(*mcsv1a1.ServiceImport))
}

func (c *Controller) serviceImportDeleted(obj interface{}) {
	klog.V(log.DEBUG).Infof("In serviceImportDeleted for: %#v, ", obj)

	var si *mcsv1a1.ServiceImport
	var ok bool
	if si, ok = obj.(*mcsv1a1.ServiceImport); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %#v to DeletedFinalStateUnknown", obj)
			return
		}

		si, ok = tombstone.Obj.(*mcsv1a1.ServiceImport)
		if !ok {
			klog.Errorf("Could not convert object tombstone %#v to Unstructured", tombstone.Obj)
			return
		}
	}

	c.store.Remove(si)
}
