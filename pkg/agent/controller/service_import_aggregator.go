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
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newServiceImportAggregator(brokerClient dynamic.Interface, brokerNamespace, clusterID string, scheme *runtime.Scheme,
) *ServiceImportAggregator {
	return &ServiceImportAggregator{
		clusterID:       clusterID,
		converter:       converter{scheme: scheme},
		brokerClient:    brokerClient,
		brokerNamespace: brokerNamespace,
	}
}

func (a *ServiceImportAggregator) updateOnCreateOrUpdate(ctx context.Context, name, namespace string) error {
	return a.update(ctx, name, namespace, func(existing *mcsv1a1.ServiceImport) error {
		return a.setServicePorts(ctx, existing)
	})
}

func (a *ServiceImportAggregator) setServicePorts(ctx context.Context, si *mcsv1a1.ServiceImport) error {
	// We don't set the port info for headless services.
	if si.Spec.Type != mcsv1a1.ClusterSetIP {
		return nil
	}

	serviceName := si.Annotations[mcsv1a1.LabelServiceName]
	serviceNamespace := si.Annotations[constants.LabelSourceNamespace]

	list, err := a.brokerClient.Resource(endpointSliceGVR).Namespace(a.brokerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			discovery.LabelManagedBy:       constants.LabelValueManagedBy,
			constants.LabelSourceNamespace: serviceNamespace,
			mcsv1a1.LabelServiceName:       serviceName,
		}).String(),
	})
	if err != nil {
		return errors.Wrapf(err, "error listing the EndpointSlices associated with service %s/%s",
			serviceNamespace, serviceName)
	}

	si.Spec.Ports = make([]mcsv1a1.ServicePort, 0)

	for i := range list.Items {
		eps := a.converter.toEndpointSlice(&list.Items[i])
		si.Spec.Ports = slices.Union(si.Spec.Ports, a.converter.toServicePorts(eps.Ports), servicePortKey)
	}

	logger.V(log.DEBUG).Infof("Calculated ports for aggregated ServiceImport %q: %#v", si.Name, si.Spec.Ports)

	return nil
}

func (a *ServiceImportAggregator) updateOnDelete(ctx context.Context, name, namespace string) error {
	return a.update(ctx, name, namespace, func(existing *mcsv1a1.ServiceImport) error {
		var removed bool

		existing.Status.Clusters, removed = slices.Remove(existing.Status.Clusters, mcsv1a1.ClusterStatus{Cluster: a.clusterID},
			clusterStatusKey)
		if !removed {
			return nil
		}

		logger.V(log.DEBUG).Infof("Removed cluster name %q from aggregated ServiceImport %q. New status: %#v",
			a.clusterID, existing.Name, existing.Status.Clusters)

		return a.setServicePorts(ctx, existing)
	})
}

func (a *ServiceImportAggregator) update(ctx context.Context, name, namespace string, mutate func(*mcsv1a1.ServiceImport) error) error {
	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", name, namespace),
		},
	}

	//nolint:wrapcheck // Let the caller wrap it
	return util.Update(ctx, resource.ForDynamic(a.brokerServiceImportClient()),
		a.converter.toUnstructured(aggregate),
		func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			existing := a.converter.toServiceImport(obj)

			err := mutate(existing)
			if err != nil {
				return nil, err
			}

			if len(existing.Status.Clusters) == 0 {
				logger.V(log.DEBUG).Infof("Deleting aggregated ServiceImport %q", existing.Name)

				err := a.brokerServiceImportClient().Delete(ctx, existing.Name, metav1.DeleteOptions{
					Preconditions: &metav1.Preconditions{
						ResourceVersion: ptr.To(existing.ResourceVersion),
					},
				})
				if apierrors.IsNotFound(err) {
					err = nil
				}

				return obj, errors.Wrapf(err, "error deleting aggregated ServiceImport %q", existing.Name)
			}

			return a.converter.toUnstructured(existing), nil
		})
}

func (a *ServiceImportAggregator) brokerServiceImportClient() dynamic.ResourceInterface {
	return a.brokerClient.Resource(serviceImportGVR).Namespace(a.brokerNamespace)
}

func clusterStatusKey(c mcsv1a1.ClusterStatus) string {
	return c.Cluster
}

func servicePortKey(p mcsv1a1.ServicePort) string {
	return fmt.Sprintf("%s%s%d", p.Name, p.Protocol, p.Port)
}
