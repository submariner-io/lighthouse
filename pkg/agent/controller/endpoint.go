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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	utilnet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func startEndpointController(localClient dynamic.Interface, restMapper meta.RESTMapper, scheme *runtime.Scheme,
	serviceImport *mcsv1a1.ServiceImport, serviceImportNameSpace, serviceName, clusterID string,
	globalIngressIPCache *globalIngressIPCache,
) (*EndpointController, error) {
	logger.V(log.DEBUG).Infof("Starting Endpoints controller for service %s/%s", serviceImportNameSpace, serviceName)

	globalIngressIPGVR, _ := schema.ParseResourceArg("globalingressips.v1.submariner.io")

	controller := &EndpointController{
		clusterID:                    clusterID,
		serviceImportUID:             serviceImport.UID,
		serviceImportName:            serviceImport.Name,
		serviceImportSourceNameSpace: serviceImportNameSpace,
		serviceName:                  serviceName,
		stopCh:                       make(chan struct{}),
		isHeadless:                   serviceImport.Spec.Type == mcsv1a1.Headless,
		globalIngressIPCache:         globalIngressIPCache,
		localClient:                  localClient,
		ingressIPClient:              localClient.Resource(*globalIngressIPGVR),
	}

	nameSelector := fields.OneTermEqualSelector("metadata.name", serviceName)

	epsSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Endpoints -> EndpointSlice",
		SourceClient:        localClient,
		SourceNamespace:     serviceImportNameSpace,
		SourceFieldSelector: nameSelector.String(),
		Direction:           syncer.LocalToRemote,
		RestMapper:          restMapper,
		Federator:           broker.NewFederator(localClient, restMapper, serviceImportNameSpace, "", "ownerReferences"),
		ResourceType:        &corev1.Endpoints{},
		Transform:           controller.endpointsToEndpointSlice,
		Scheme:              scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating Endpoints syncer")
	}

	if err := epsSyncer.Start(controller.stopCh); err != nil {
		return nil, errors.Wrap(err, "error starting Endpoints syncer")
	}

	return controller, nil
}

func (e *EndpointController) stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
		e.cleanup()
	})
}

func (e *EndpointController) cleanup() {
	resourceClient := e.localClient.Resource(schema.GroupVersionResource{
		Group:    discovery.SchemeGroupVersion.Group,
		Version:  discovery.SchemeGroupVersion.Version,
		Resource: "endpointslices",
	}).Namespace(e.serviceImportSourceNameSpace)

	// MCS-compliant labels
	err := resourceClient.DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			constants.LabelSourceNamespace:  e.serviceImportSourceNameSpace,
			constants.MCSLabelSourceCluster: e.clusterID,
			constants.MCSLabelServiceName:   e.serviceName,
		}).String(),
	})

	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting the EndpointSlices associated with ServiceImport %q", e.serviceImportName)
	}

	// Lighthouse-proprietary labels
	err = resourceClient.DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			constants.LabelSourceNamespace:         e.serviceImportSourceNameSpace,
			constants.LighthouseLabelSourceCluster: e.clusterID,
			constants.LighthouseLabelSourceName:    e.serviceName,
		}).String(),
	})

	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting the EndpointSlices associated with ServiceImport %q", e.serviceImportName)
	}
}

func (e *EndpointController) endpointsToEndpointSlice(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	endPoints := obj.(*corev1.Endpoints)

	if op == syncer.Delete {
		logger.V(log.DEBUG).Infof("Endpoints %s/%s deleted", endPoints.Namespace, endPoints.Name)

		return &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e.endpointSliceNameFrom(endPoints),
				Namespace: endPoints.Namespace,
			},
		}, false
	}

	if op == syncer.Create {
		logger.V(log.DEBUG).Infof("Endpoints %s/%s created", endPoints.Namespace, endPoints.Name)
	} else {
		logger.V(log.TRACE).Infof("Endpoints %s/%s updated", endPoints.Namespace, endPoints.Name)
	}

	return e.endpointSliceFromEndpoints(endPoints, op)
}

func (e *EndpointController) endpointSliceFromEndpoints(endpoints *corev1.Endpoints, op syncer.Operation) (
	runtime.Object, bool,
) {
	endpointSlice := &discovery.EndpointSlice{}

	endpointSlice.Name = e.endpointSliceNameFrom(endpoints)
	endpointSlice.Labels = map[string]string{
		discovery.LabelManagedBy:        constants.LabelValueManagedBy,
		constants.LabelSourceNamespace:  e.serviceImportSourceNameSpace,
		constants.MCSLabelSourceCluster: e.clusterID,
		constants.MCSLabelServiceName:   e.serviceName,
	}

	endpointSlice.AddressType = discovery.AddressTypeIPv4

	if len(endpoints.Subsets) > 0 {
		subset := endpoints.Subsets[0]
		for i := range subset.Ports {
			endpointSlice.Ports = append(endpointSlice.Ports, discovery.EndpointPort{
				Port:     &subset.Ports[i].Port,
				Name:     &subset.Ports[i].Name,
				Protocol: &subset.Ports[i].Protocol,
			})
		}

		if allAddressesIPv6(subset.Addresses) {
			endpointSlice.AddressType = discovery.AddressTypeIPv6
		}

		newEndpoints, retry := e.getEndpointsFromAddresses(subset.Addresses, endpointSlice.AddressType, true)
		if retry {
			return nil, true
		}

		endpointSlice.Endpoints = append(endpointSlice.Endpoints, newEndpoints...)
	}

	if op == syncer.Create {
		logger.V(log.DEBUG).Infof("Returning EndpointSlice: %#v", endpointSlice)
	} else {
		logger.V(log.TRACE).Infof("Returning EndpointSlice: %#v", endpointSlice)
	}

	return endpointSlice, false
}

func (e *EndpointController) endpointSliceNameFrom(endpoints *corev1.Endpoints) string {
	return endpoints.Name + "-" + endpoints.Namespace + "-" + e.clusterID
}

func (e *EndpointController) getEndpointsFromAddresses(addresses []corev1.EndpointAddress, addressType discovery.AddressType,
	ready bool,
) ([]discovery.Endpoint, bool) {
	endpoints := []discovery.Endpoint{}
	isIPv6AddressType := addressType == discovery.AddressTypeIPv6

	for i := range addresses {
		address := &addresses[i]
		if utilnet.IsIPv6String(address.IP) == isIPv6AddressType {
			endpoint, retry := e.endpointFromAddress(address, ready)
			if retry {
				return nil, true
			}

			endpoints = append(endpoints, *endpoint)
		}
	}

	return endpoints, false
}

func (e *EndpointController) endpointFromAddress(address *corev1.EndpointAddress, ready bool) (*discovery.Endpoint, bool) {
	ip := e.getIP(address)

	if ip == "" {
		return nil, true
	}

	endpoint := &discovery.Endpoint{
		Addresses:  []string{ip},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		NodeName:   address.NodeName,
	}

	/*
		We only need TargetRef.Name as pod address and hostname are only relevant fields.
		Avoid copying TargetRef coz it it has revision which can change for reasons other
		than address and hostname, resulting in unnecessary syncs across clusters.
		Revisit this logic if we need TargetRef for other use cases.
	*/

	switch {
	case address.Hostname != "":
		endpoint.Hostname = &address.Hostname
	case address.TargetRef != nil:
		endpoint.Hostname = &address.TargetRef.Name
	}

	return endpoint, false
}

func allAddressesIPv6(addresses []corev1.EndpointAddress) bool {
	if len(addresses) == 0 {
		return false
	}

	for _, address := range addresses {
		if !utilnet.IsIPv6String(address.IP) {
			return false
		}
	}

	return true
}

func (e *EndpointController) getIP(address *corev1.EndpointAddress) string {
	if e.isHeadless && e.globalIngressIPCache != nil {
		obj, found := e.globalIngressIPCache.getForPod(e.serviceImportSourceNameSpace, address.TargetRef.Name)

		var ip string
		if found {
			ip, _, _ = unstructured.NestedString(obj.Object, "status", "allocatedIP")
		}

		if ip == "" {
			logger.Infof("GlobalIP for EndpointAddress %q is not allocated yet", address.TargetRef.Name)
		}

		return ip
	}

	return address.IP
}
