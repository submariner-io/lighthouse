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
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var (
	serviceImportGVR = schema.GroupVersionResource{
		Group:    mcsv1a1.GroupName,
		Version:  mcsv1a1.SchemeGroupVersion.Version,
		Resource: "serviceimports",
	}

	endpointSliceGVR = schema.GroupVersionResource{
		Group:    discovery.GroupName,
		Version:  discovery.SchemeGroupVersion.Version,
		Resource: "endpointslices",
	}
)

func (a *Controller) Cleanup() error {
	// Delete all ServiceImports from the local cluster skipping those in the broker namespace if the broker is on the
	// local cluster.
	err := a.serviceImportSyncer.GetLocalClient().Resource(serviceImportGVR).Namespace(metav1.NamespaceAll).DeleteCollection(context.TODO(),
		metav1.DeleteOptions{},
		metav1.ListOptions{
			FieldSelector: fields.OneTermNotEqualSelector("metadata.namespace", a.serviceImportSyncer.GetBrokerNamespace()).String(),
		})
	if err != nil {
		return errors.Wrap(err, "error deleting local ServiceImports")
	}

	// Delete all local ServiceImports from the broker.
	err = a.serviceImportSyncer.GetBrokerClient().Resource(serviceImportGVR).Namespace(a.serviceImportSyncer.GetBrokerNamespace()).
		DeleteCollection(context.TODO(), metav1.DeleteOptions{},
			metav1.ListOptions{
				LabelSelector: labels.Set(map[string]string{lhconstants.LighthouseLabelSourceCluster: a.clusterID}).String(),
			})
	if err != nil {
		return errors.Wrap(err, "error deleting remote EndpointSlices")
	}

	// Delete all EndpointSlices from the local cluster skipping those in the broker namespace if the broker is on the
	// local cluster.
	err = a.endpointSliceSyncer.GetLocalClient().Resource(endpointSliceGVR).Namespace(metav1.NamespaceAll).DeleteCollection(context.TODO(),
		metav1.DeleteOptions{},
		metav1.ListOptions{
			FieldSelector: fields.OneTermNotEqualSelector("metadata.namespace", a.serviceImportSyncer.GetBrokerNamespace()).String(),
			LabelSelector: labels.Set(map[string]string{discovery.LabelManagedBy: lhconstants.LabelValueManagedBy}).String(),
		})
	if err != nil {
		return errors.Wrap(err, "error deleting local EndpointSlices")
	}

	// Delete all local EndpointSlices from the broker.
	err = a.endpointSliceSyncer.GetBrokerClient().Resource(endpointSliceGVR).Namespace(a.endpointSliceSyncer.GetBrokerNamespace()).
		DeleteCollection(context.TODO(), metav1.DeleteOptions{},
			metav1.ListOptions{
				LabelSelector: labels.Set(map[string]string{lhconstants.MCSLabelSourceCluster: a.clusterID}).String(),
			})

	return errors.Wrap(err, "error deleting remote EndpointSlices")
}
