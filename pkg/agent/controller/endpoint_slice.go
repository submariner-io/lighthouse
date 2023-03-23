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
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newEndpointSliceController(spec *AgentSpecification, syncerConfig broker.SyncerConfig,
	serviceExportClient *ServiceExportClient,
) (*EndpointSliceController, error) {
	c := &EndpointSliceController{
		clusterID:           spec.ClusterID,
		serviceExportClient: serviceExportClient,
	}

	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			LocalSourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
				discovery.LabelManagedBy: constants.LabelValueManagedBy,
			}).String(),
			LocalResourceType:     &discovery.EndpointSlice{},
			LocalTransform:        c.onLocalEndpointSlice,
			LocalOnSuccessfulSync: c.onLocalEndpointSliceSynced,
			BrokerResourceType:    &discovery.EndpointSlice{},
			BrokerTransform:       c.onRemoteEndpointSlice,
		},
	}

	var err error

	c.syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating EndpointSlice syncer")
	}

	c.serviceImportAggregator = newServiceImportAggregator(c.syncer.GetBrokerClient(), c.syncer.GetBrokerNamespace(),
		spec.ClusterID, syncerConfig.Scheme)

	return c, nil
}

func (c *EndpointSliceController) start(stopCh <-chan struct{}) error {
	if err := c.syncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting EndpointSlice syncer")
	}

	return nil
}

func (c *EndpointSliceController) onLocalEndpointSlice(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	labels := endpointSlice.GetObjectMeta().GetLabels()

	oldName := labels[mcsv1a1.LabelServiceName] + "-" + labels[constants.MCSLabelSourceCluster]
	if op != syncer.Delete && endpointSlice.Name == oldName {
		logger.Infof("EndpointSlice %s/%s has the old naming convention sans namespace - deleting it",
			endpointSlice.Namespace, endpointSlice.Name)

		err := c.syncer.GetLocalFederator().Delete(endpointSlice)
		if err != nil {
			logger.Errorf(err, "Error deleting local EndpointSlice %s/%s", endpointSlice.Namespace, endpointSlice.Name)
		}

		return nil, false
	}

	return obj, false
}

func (c *EndpointSliceController) onRemoteEndpointSlice(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	endpointSlice.Namespace = endpointSlice.GetObjectMeta().GetLabels()[constants.LabelSourceNamespace]

	return endpointSlice, false
}

func (c *EndpointSliceController) onLocalEndpointSliceSynced(obj runtime.Object, op syncer.Operation) bool {
	endpointSlice := obj.(*discovery.EndpointSlice)

	serviceName := endpointSlice.Labels[mcsv1a1.LabelServiceName]
	serviceNamespace := endpointSlice.Labels[constants.LabelSourceNamespace]

	logger.V(log.DEBUG).Infof("Local EndpointSlice for service \"%s/%s\" %sd on broker", serviceNamespace, serviceName, op)

	var err error

	if op == syncer.Delete {
		err = c.serviceImportAggregator.updateOnDelete(serviceName, serviceNamespace)
	} else {
		err = c.serviceImportAggregator.updateOnCreateOrUpdate(serviceName, serviceNamespace)
		if err != nil {
			c.serviceExportClient.updateStatusConditions(serviceName, serviceNamespace, newServiceExportCondition(constants.ServiceExportSynced,
				corev1.ConditionFalse, exportFailedReason, fmt.Sprintf("Unable to export: %v", err)))
		} else {
			c.serviceExportClient.updateStatusConditions(serviceName, serviceNamespace, newServiceExportCondition(constants.ServiceExportSynced,
				corev1.ConditionTrue, "", "Service was successfully exported to the broker"))
		}
	}

	if err != nil {
		logger.Errorf(err, "Error processing %sd EndpointSlice for service \"%s/%s\"", op, serviceNamespace, serviceName)
	}

	return err != nil
}
