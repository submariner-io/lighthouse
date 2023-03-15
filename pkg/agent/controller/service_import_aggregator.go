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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/pointer"
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

func (a *ServiceImportAggregator) updateOnCreateOrUpdate(name, namespace string) error {
	return a.update(name, namespace, func(existing *mcsv1a1.ServiceImport) {
		var added bool

		existing.Status.Clusters, added = slices.AppendIfNotPresent(existing.Status.Clusters,
			mcsv1a1.ClusterStatus{Cluster: a.clusterID}, clusterStatusKey)

		if added {
			logger.V(log.DEBUG).Infof("Added cluster name %q to aggregated ServiceImport %q. New status: %#v",
				a.clusterID, existing.Name, existing.Status.Clusters)
		}

		// TODO - merge port info
	})
}

func (a *ServiceImportAggregator) updateOnDelete(name, namespace string) error {
	return a.update(name, namespace, func(existing *mcsv1a1.ServiceImport) {
		var removed bool

		existing.Status.Clusters, removed = slices.Remove(existing.Status.Clusters, mcsv1a1.ClusterStatus{Cluster: a.clusterID},
			clusterStatusKey)
		if !removed {
			return
		}

		logger.V(log.DEBUG).Infof("Removed cluster name %q from aggregated ServiceImport %q. New status: %#v",
			a.clusterID, existing.Name, existing.Status.Clusters)

		// TODO - remove port info
	})
}

func (a *ServiceImportAggregator) update(name, namespace string, mutate func(*mcsv1a1.ServiceImport)) error {
	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", name, namespace),
		},
	}

	//nolint:wrapcheck // No need to wrap the return error.
	return util.Update(context.Background(), resource.ForDynamic(a.brokerServiceImportClient()), a.converter.toUnstructured(aggregate),
		func(obj runtime.Object) (runtime.Object, error) {
			existing := a.converter.toServiceImport(obj)

			mutate(existing)

			if len(existing.Status.Clusters) == 0 {
				logger.V(log.DEBUG).Infof("Deleting aggregated ServiceImport %q", existing.Name)

				err := a.brokerServiceImportClient().Delete(context.Background(), existing.Name, metav1.DeleteOptions{
					Preconditions: &metav1.Preconditions{
						ResourceVersion: pointer.String(existing.ResourceVersion),
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
