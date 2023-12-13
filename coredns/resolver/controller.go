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

package resolver

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/lighthouse/coredns/constants"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var logger = log.Logger{Logger: logf.Log.WithName("Resolver")}

type controller struct {
	resolver        *Interface
	resourceWatcher watcher.Interface
	stopCh          chan struct{}
}

func NewController(r *Interface) *controller {
	return &controller{
		resolver: r,
		stopCh:   make(chan struct{}),
	}
}

func (c *controller) Start(config watcher.Config) error {
	logger.Infof("Starting Resolver Controller")

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:                "EndpointSlice watcher",
			ResourceType:        &discovery.EndpointSlice{},
			SourceNamespace:     metav1.NamespaceAll,
			SourceLabelSelector: labels.Set(map[string]string{discovery.LabelManagedBy: constants.LabelValueManagedBy}).String(),
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: c.onEndpointSliceCreateOrUpdate,
				OnUpdateFunc: c.onEndpointSliceCreateOrUpdate,
				OnDeleteFunc: c.onEndpointSliceDelete,
			},
		},
		{
			Name:            "ServiceImport watcher",
			ResourceType:    &mcsv1a1.ServiceImport{},
			SourceNamespace: metav1.NamespaceAll,
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: c.onServiceImportCreateOrUpdate,
				OnUpdateFunc: c.onServiceImportCreateOrUpdate,
				OnDeleteFunc: c.onServiceImportDelete,
			},
		},
	}

	var err error

	c.resourceWatcher, err = watcher.New(&config)
	if err != nil {
		return errors.Wrap(err, "error creating the resource watcher")
	}

	err = c.resourceWatcher.Start(c.stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the resource watcher")
	}

	return nil
}

func (c *controller) Stop() {
	close(c.stopCh)

	logger.Infof("Resolver Controller stopped")
}

func (c *controller) onEndpointSliceCreateOrUpdate(obj runtime.Object, _ int) bool {
	endpointSlice := obj.(*discovery.EndpointSlice)
	if c.ignoreEndpointSlice(endpointSlice) {
		return false
	}

	if !isHeadless(endpointSlice) || isLegacyEndpointSlice(endpointSlice) {
		return c.resolver.PutEndpointSlices(endpointSlice)
	}

	return c.resolver.PutEndpointSlices(c.getAllEndpointSlices(endpointSlice)...)
}

func (c *controller) getAllEndpointSlices(forEPS *discovery.EndpointSlice) []*discovery.EndpointSlice {
	list := c.resourceWatcher.ListResources(&discovery.EndpointSlice{}, k8slabels.SelectorFromSet(map[string]string{
		constants.LabelSourceNamespace:  forEPS.Labels[constants.LabelSourceNamespace],
		mcsv1a1.LabelServiceName:        forEPS.Labels[mcsv1a1.LabelServiceName],
		constants.MCSLabelSourceCluster: forEPS.Labels[constants.MCSLabelSourceCluster],
	}))

	var epSlices []*discovery.EndpointSlice
	for i := range list {
		eps := list[i].(*discovery.EndpointSlice)
		if !isOnBroker(eps) && !isLegacyEndpointSlice(eps) {
			epSlices = append(epSlices, eps)
		}
	}

	return epSlices
}

func (c *controller) onEndpointSliceDelete(obj runtime.Object, _ int) bool {
	endpointSlice := obj.(*discovery.EndpointSlice)
	if c.ignoreEndpointSlice(endpointSlice) {
		return false
	}

	if !isHeadless(endpointSlice) {
		c.resolver.RemoveEndpointSlice(endpointSlice)
	}

	epSlices := c.getAllEndpointSlices(endpointSlice)
	if len(epSlices) == 0 {
		c.resolver.RemoveEndpointSlice(endpointSlice)
	}

	return c.resolver.PutEndpointSlices(epSlices...)
}

func (c *controller) onServiceImportCreateOrUpdate(obj runtime.Object, _ int) bool {
	c.resolver.PutServiceImport(obj.(*mcsv1a1.ServiceImport))
	return false
}

func (c *controller) onServiceImportDelete(obj runtime.Object, _ int) bool {
	c.resolver.RemoveServiceImport(obj.(*mcsv1a1.ServiceImport))
	return false
}

func (c *controller) ignoreEndpointSlice(eps *discovery.EndpointSlice) bool {
	return isOnBroker(eps) || (isLegacyEndpointSlice(eps) && len(c.getAllEndpointSlices(eps)) > 0)
}

func isOnBroker(eps *discovery.EndpointSlice) bool {
	return eps.Namespace != eps.Labels[constants.LabelSourceNamespace]
}

func isLegacyEndpointSlice(eps *discovery.EndpointSlice) bool {
	// Any EndpointSlice's name prior to 0.16 was suffixed with the cluster ID.
	return strings.HasSuffix(eps.Name, "-"+eps.Labels[constants.MCSLabelSourceCluster])
}
