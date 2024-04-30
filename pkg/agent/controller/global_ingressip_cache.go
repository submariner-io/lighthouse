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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/watcher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

//nolint:gocritic // (hugeParam) This function modifies config so we don't want to pass by pointer.
func newGlobalIngressIPCache(config watcher.Config) (*globalIngressIPCache, error) {
	c := &globalIngressIPCache{
		byService: globalIngressIPMap{
			entries: map[string]*globalIngressIPEntry{},
		},
		byPod: globalIngressIPMap{
			entries: map[string]*globalIngressIPEntry{},
		},
		byEndpoints: globalIngressIPMap{
			entries: map[string]*globalIngressIPEntry{},
		},
	}

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
	c.applyToCache(obj, func(to *globalIngressIPMap, key string, obj *unstructured.Unstructured) {
		var onAddOrUpdate func()

		to.Lock()

		e := to.entries[key]
		if e == nil {
			e = &globalIngressIPEntry{}
			to.entries[key] = e
		}

		e.obj = obj
		onAddOrUpdate = e.onAddOrUpdate

		to.Unlock()

		if onAddOrUpdate != nil {
			onAddOrUpdate()
		}
	})
}

func (c *globalIngressIPCache) onDelete(obj *unstructured.Unstructured) {
	c.applyToCache(obj, func(to *globalIngressIPMap, key string, _ *unstructured.Unstructured) {
		to.Lock()
		defer to.Unlock()

		delete(to.entries, key)
	})
}

func (c *globalIngressIPCache) applyToCache(obj *unstructured.Unstructured,
	apply func(to *globalIngressIPMap, key string, obj *unstructured.Unstructured),
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

func (c *globalIngressIPCache) getForService(namespace, name string, transform globalIngressIPTransformFn, onAddOrUpdate func(),
) (any, bool) {
	return c.get(&c.byService, namespace, name, transform, onAddOrUpdate)
}

func (c *globalIngressIPCache) getForPod(namespace, name string, transform globalIngressIPTransformFn, onAddOrUpdate func(),
) (any, bool) {
	return c.get(&c.byPod, namespace, name, transform, onAddOrUpdate)
}

func (c *globalIngressIPCache) getForEndpoints(namespace, ip string, transform globalIngressIPTransformFn, onAddOrUpdate func(),
) (any, bool) {
	return c.get(&c.byEndpoints, namespace, ip, transform, onAddOrUpdate)
}

func (c *globalIngressIPCache) get(from *globalIngressIPMap, namespace, name string, transform globalIngressIPTransformFn,
	onAddOrUpdate func(),
) (any, bool) {
	from.Lock()
	defer from.Unlock()

	key := c.key(namespace, name)

	e := from.entries[key]
	if e == nil {
		e = &globalIngressIPEntry{}
		from.entries[key] = e
	}

	if e.obj == nil {
		e.onAddOrUpdate = onAddOrUpdate
		return nil, false
	}

	r, ok := transform(e.obj)
	if !ok {
		e.onAddOrUpdate = onAddOrUpdate
	} else {
		e.onAddOrUpdate = nil
	}

	return r, ok
}

func (c *globalIngressIPCache) key(ns, n string) string {
	return ns + "/" + n
}
