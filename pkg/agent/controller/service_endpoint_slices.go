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
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/pointer"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func startEndpointSliceController(localClient dynamic.Interface, restMapper meta.RESTMapper, scheme *runtime.Scheme,
	serviceImport *mcsv1a1.ServiceImport, clusterID string, globalIngressIPCache *globalIngressIPCache,
) (*ServiceEndpointSliceController, error) {
	serviceNamespace := serviceImport.Labels[constants.LabelSourceNamespace]
	serviceName := serviceImportSourceName(serviceImport)

	logger.V(log.DEBUG).Infof("Starting EndpointSlice controller for service %s/%s", serviceNamespace, serviceName)

	globalIngressIPGVR, _ := schema.ParseResourceArg("globalingressips.v1.submariner.io")

	controller := &ServiceEndpointSliceController{
		clusterID:                clusterID,
		serviceNamespace:         serviceNamespace,
		serviceName:              serviceName,
		serviceImportSpec:        &serviceImport.Spec,
		publishNotReadyAddresses: serviceImport.Annotations[constants.PublishNotReadyAddresses],
		stopCh:                   make(chan struct{}),
		globalIngressIPCache:     globalIngressIPCache,
		localClient:              localClient,
		ingressIPClient:          localClient.Resource(*globalIngressIPGVR),
		federator:                federate.NewCreateOrUpdateFederator(localClient, restMapper, serviceNamespace, ""),
	}

	var err error

	controller.epsSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "K8s EndpointSlice -> LH EndpointSlice",
		SourceClient:    localClient,
		SourceNamespace: serviceNamespace,
		SourceLabelSelector: labels.Set(map[string]string{
			discovery.LabelServiceName: serviceName,
		}).String(),
		RestMapper:   restMapper,
		Federator:    controller,
		ResourceType: &discovery.EndpointSlice{},
		Transform:    controller.onServiceEndpointSlice,
		Scheme:       scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating Endpoints syncer")
	}

	if err := controller.epsSyncer.Start(controller.stopCh); err != nil {
		return nil, errors.Wrap(err, "error starting Endpoints syncer")
	}

	return controller, nil
}

func (c *ServiceEndpointSliceController) stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.epsSyncer.AwaitStopped()
	})
}

func (c *ServiceEndpointSliceController) cleanup() (bool, error) {
	resourceClient := c.localClient.Resource(schema.GroupVersionResource{
		Group:    discovery.SchemeGroupVersion.Group,
		Version:  discovery.SchemeGroupVersion.Version,
		Resource: "endpointslices",
	}).Namespace(c.serviceNamespace)

	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			discovery.LabelManagedBy:        constants.LabelValueManagedBy,
			constants.LabelSourceNamespace:  c.serviceNamespace,
			constants.MCSLabelSourceCluster: c.clusterID,
			mcsv1a1.LabelServiceName:        c.serviceName,
		}).String(),
	}

	list, err := resourceClient.List(context.Background(), listOptions)
	if err != nil {
		return false, errors.Wrapf(err, "error listing the EndpointSlices associated with service %s/%s",
			c.serviceNamespace, c.serviceName)
	}

	if len(list.Items) == 0 {
		return false, nil
	}

	err = resourceClient.DeleteCollection(context.Background(), metav1.DeleteOptions{}, listOptions)

	if err != nil && !apierrors.IsNotFound(err) {
		return false, errors.Wrapf(err, "error deleting the EndpointSlices associated with service %s/%s",
			c.serviceNamespace, c.serviceName)
	}

	return true, nil
}

func (c *ServiceEndpointSliceController) onServiceEndpointSlice(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	serviceEPS := obj.(*discovery.EndpointSlice)

	logLevel := log.DEBUG
	if op == syncer.Update {
		logLevel = log.TRACE
	}

	logger.V(logLevel).Infof("Service EndpointSlice %s/%s %sd", serviceEPS.Namespace, serviceEPS.Name, op)

	var (
		returnEPS *discovery.EndpointSlice
		requeue   bool
	)

	if c.isHeadless() {
		returnEPS, requeue = c.headlessEndpointSliceFrom(serviceEPS, op)
	} else {
		returnEPS = c.clusterIPEndpointSliceFrom(serviceEPS)
	}

	if requeue || returnEPS == nil {
		return nil, requeue
	}

	logger.V(logLevel).Infof("Returning EndpointSlice %s/%s: %s", serviceEPS.Namespace, returnEPS.Name,
		endpointSliceStringer{returnEPS})

	return returnEPS, false
}

func (c *ServiceEndpointSliceController) clusterIPEndpointSliceFrom(serviceEPS *discovery.EndpointSlice) *discovery.EndpointSlice {
	endpointSlice := c.newEndpointSliceFrom(serviceEPS)

	endpointSlice.Endpoints = []discovery.Endpoint{{
		Addresses: []string{c.serviceImportSpec.IPs[0]},
		Conditions: discovery.EndpointConditions{
			Ready: pointer.Bool(c.getReadyAddressCount() > 0),
		},
	}}

	for i := range c.serviceImportSpec.Ports {
		endpointSlice.Ports = append(endpointSlice.Ports, discovery.EndpointPort{
			Port:        &c.serviceImportSpec.Ports[i].Port,
			Name:        &c.serviceImportSpec.Ports[i].Name,
			Protocol:    &c.serviceImportSpec.Ports[i].Protocol,
			AppProtocol: c.serviceImportSpec.Ports[i].AppProtocol,
		})
	}

	return endpointSlice
}

func (c *ServiceEndpointSliceController) getReadyAddressCount() int {
	list := c.epsSyncer.ListResources()

	readyCount := 0

	for _, o := range list {
		endpoints := o.(*discovery.EndpointSlice).Endpoints
		for i := range endpoints {
			// Note: we're treating nil as ready to be on the safe side as the EndpointConditions doc states
			// "In most cases consumers should interpret this unknown state (ie nil) as ready".
			if endpoints[i].Conditions.Ready == nil || *endpoints[i].Conditions.Ready {
				readyCount++
			}
		}
	}

	return readyCount
}

func (c *ServiceEndpointSliceController) headlessEndpointSliceFrom(serviceEPS *discovery.EndpointSlice, op syncer.Operation) (
	*discovery.EndpointSlice, bool,
) {
	endpointSlice := c.newEndpointSliceFrom(serviceEPS)

	if op == syncer.Delete {
		return endpointSlice, false
	}

	endpointSlice.Ports = serviceEPS.Ports
	endpointSlice.Endpoints = make([]discovery.Endpoint, len(serviceEPS.Endpoints))

	for i := range serviceEPS.Endpoints {
		endpointSlice.Endpoints[i] = serviceEPS.Endpoints[i]
		endpointSlice.Endpoints[i].Addresses = c.getHeadlessEndpointAddresses(&serviceEPS.Endpoints[i])

		if len(endpointSlice.Endpoints[i].Addresses) == 0 {
			return nil, true
		}
	}

	return endpointSlice, false
}

func (c *ServiceEndpointSliceController) newEndpointSliceFrom(serviceEPS *discovery.EndpointSlice) *discovery.EndpointSlice {
	name := c.serviceName
	if c.isHeadless() {
		name = serviceEPS.Name
	}

	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s-%s", name, serviceEPS.Namespace, c.clusterID),
			Labels: map[string]string{
				discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				constants.LabelSourceNamespace:  c.serviceNamespace,
				constants.MCSLabelSourceCluster: c.clusterID,
				mcsv1a1.LabelServiceName:        c.serviceName,
				constants.LabelIsHeadless:       strconv.FormatBool(c.isHeadless()),
			},
			Annotations: map[string]string{
				constants.PublishNotReadyAddresses: c.publishNotReadyAddresses,
				constants.GlobalnetEnabled:         strconv.FormatBool(c.isHeadless() && c.globalIngressIPCache != nil),
			},
		},
		AddressType: serviceEPS.AddressType,
	}
}

func (c *ServiceEndpointSliceController) getHeadlessEndpointAddresses(endpoint *discovery.Endpoint) []string {
	if c.globalIngressIPCache == nil {
		return endpoint.Addresses
	}

	var (
		obj    *unstructured.Unstructured
		found  bool
		ip     string
		forPod bool
	)

	if endpoint.TargetRef != nil && endpoint.TargetRef.Kind == "Pod" {
		forPod = true
		obj, found = c.globalIngressIPCache.getForPod(c.serviceNamespace, endpoint.TargetRef.Name)
	} else {
		forPod = false
		obj, found = c.globalIngressIPCache.getForEndpoints(c.serviceNamespace, endpoint.Addresses[0])
	}

	if found {
		ip, _, _ = unstructured.NestedString(obj.Object, "status", "allocatedIP")
	}

	if ip == "" {
		if forPod {
			logger.Infof("GlobalIP for Endpoint pod name %q is not allocated yet", endpoint.TargetRef.Name)
		} else {
			logger.Infof("GlobalIP for Endpoint IP %q is not allocated yet", endpoint.Addresses[0])
		}

		return nil
	}

	return []string{ip}
}

func (c *ServiceEndpointSliceController) isHeadless() bool {
	return c.serviceImportSpec.Type == mcsv1a1.Headless
}

func (c *ServiceEndpointSliceController) Distribute(obj runtime.Object) error {
	return c.federator.Distribute(obj) //nolint:wrapcheck // No need to wrap here
}

func (c *ServiceEndpointSliceController) Delete(obj runtime.Object) error {
	if c.isHeadless() {
		return c.federator.Delete(obj) //nolint:wrapcheck // No need to wrap here
	}

	// For a non-headless service, we never delete the single exported EPS - we update its endpoint condition based on
	// the backend service EPS's as they are created/updated/deleted.
	return c.Distribute(obj)
}

type endpointSliceStringer struct {
	*discovery.EndpointSlice
}

func (s endpointSliceStringer) String() string {
	ports, _ := json.MarshalIndent(&s.Ports, "", "  ")
	endpoints, _ := json.MarshalIndent(&s.Endpoints, "", "  ")

	return fmt.Sprintf("endpoints: %s\nports: %s", endpoints, ports)
}
