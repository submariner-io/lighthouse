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

package controller

import (
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/watcher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

//nolint:gocritic // (hugeParam) This function modifies config so we don't want to pass by pointer.
func newGlobalIngressIPCache(config watcher.Config) (*globalIngressIPCache, error) {
	c := &globalIngressIPCache{}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "GlobalIngressIP watcher",
			ResourceType: GetGlobalIngressIPObj(),
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: func(obj runtime.Object, _ int) bool {
					c.onCreateOrUpdate(obj.(*unstructured.Unstructured))
					return false
				},
				OnUpdateFunc: func(obj runtime.Object, _ int) bool {
					c.onCreateOrUpdate(obj.(*unstructured.Unstructured))
					return false
				},
				OnDeleteFunc: func(obj runtime.Object, _ int) bool {
					c.onDelete(obj.(*unstructured.Unstructured))
					return false
				},
			},
			SourceNamespace: metav1.NamespaceAll,
		},
	}

	var err error

	c.watcher, err = watcher.New(&config)

	return c, errors.Wrap(err, "error creating GlobalIngressIP watcher")
}

func (c *globalIngressIPCache) start(stopCh <-chan struct{}) error {
	return errors.Wrap(c.watcher.Start(stopCh), "error starting GlobalIngressIP watcher")
}

func (c *globalIngressIPCache) onCreateOrUpdate(obj *unstructured.Unstructured) {
	c.applyToCache(obj, func(to *sync.Map, key string, obj *unstructured.Unstructured) {
		to.Store(key, obj)
	})
}

func (c *globalIngressIPCache) onDelete(obj *unstructured.Unstructured) {
	c.applyToCache(obj, func(to *sync.Map, key string, _ *unstructured.Unstructured) {
		to.Delete(key)
	})
}

func (c *globalIngressIPCache) applyToCache(obj *unstructured.Unstructured,
	apply func(to *sync.Map, key string, obj *unstructured.Unstructured),
) {
	target, _, _ := unstructured.NestedString(obj.Object, "spec", "target")
	switch target {
	case ClusterIPService:
		name, _, _ := unstructured.NestedString(obj.Object, "spec", "serviceRef", "name")
		apply(&c.byService, c.key(obj.GetNamespace(), name), obj)
	case HeadlessServicePod:
		name, _, _ := unstructured.NestedString(obj.Object, "spec", "podRef", "name")
		apply(&c.byPod, c.key(obj.GetNamespace(), name), obj)
	case HeadlessServiceEndpoints:
		if ip, ok := obj.GetAnnotations()["submariner.io/headless-svc-endpoints-ip"]; ok {
			apply(&c.byEndpoints, c.key(obj.GetNamespace(), ip), obj)
		}
	}
}

func (c *globalIngressIPCache) getForService(namespace, name string) (*unstructured.Unstructured, bool) {
	return c.get(&c.byService, namespace, name)
}

func (c *globalIngressIPCache) getForPod(namespace, name string) (*unstructured.Unstructured, bool) {
	return c.get(&c.byPod, namespace, name)
}

func (c *globalIngressIPCache) getForEndpoints(namespace, ip string) (*unstructured.Unstructured, bool) {
	return c.get(&c.byEndpoints, namespace, ip)
}

func (c *globalIngressIPCache) get(from *sync.Map, namespace, name string) (*unstructured.Unstructured, bool) {
	v, found := from.Load(c.key(namespace, name))
	if !found {
		return nil, false
	}

	return v.(*unstructured.Unstructured), true
}

func (c *globalIngressIPCache) key(ns, n string) string {
	return ns + "/" + n
}
