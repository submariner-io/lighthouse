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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/workqueue"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newEndpointSliceController(spec *AgentSpecification, syncerConfig broker.SyncerConfig,
	serviceExportClient *ServiceExportClient, serviceSyncer syncer.Interface, namespaceValidator *NamespaceValidator,
) (*EndpointSliceController, error) {
	c := &EndpointSliceController{
		clusterID:           spec.ClusterID,
		serviceExportClient: serviceExportClient,
		serviceSyncer:       serviceSyncer,
		localClient:         syncerConfig.LocalClient,
		namespaceValidator:  namespaceValidator,
	}

	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.MaxLogVerbosity = global.Get("endpoint-slices.syncer.max-verbosity", 0)
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			LocalSourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
				discovery.LabelManagedBy: constants.LabelValueManagedBy,
			}).String(),
			LocalResourceType:        &discovery.EndpointSlice{},
			TransformLocalToBroker:   c.onLocalEndpointSlice,
			OnSuccessfulSyncToBroker: c.onLocalEndpointSliceSynced,
			LocalWorkQueueConfig:     workqueue.ConfigFromGlobal("local-endpoint-slices", nil),
			BrokerResourceType:       &discovery.EndpointSlice{},
			TransformBrokerToLocal:   c.onRemoteEndpointSlice,
			BrokerWorkQueueConfig:    workqueue.ConfigFromGlobal("broker-endpoint-slices", nil),
		},
	}

	var err error

	c.syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating EndpointSlice syncer")
	}

	return c, nil
}

func (c *EndpointSliceController) start(stopCh <-chan struct{}) error {
	if err := c.syncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting EndpointSlice syncer")
	}

	go func() {
		<-stopCh
	}()

	return nil
}

func (c *EndpointSliceController) onLocalEndpointSlice(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	ctx := context.TODO()

	if op != syncer.Delete && isLegacyEndpointSlice(endpointSlice) {
		logger.Infof("Found legacy EndpointSlice %s/%s - deleting it",
			endpointSlice.Namespace, endpointSlice.Name)

		err := c.syncer.GetLocalFederator().Delete(ctx, endpointSlice)
		if err != nil {
			logger.Errorf(err, "Error deleting legacy EndpointSlice %s/%s", endpointSlice.Namespace, endpointSlice.Name)
		}

		return nil, false
	}

	serviceName := endpointSlice.Labels[mcsv1a1.LabelServiceName]

	logger.Infof("Local EndpointSlice \"%s/%s\" for service %q %sd",
		endpointSlice.Namespace, endpointSlice.Name, serviceName, op)

	// Check if the associated Service exists and, if not, delete the EndpointSlice. On restart, it's possible the Service could've been
	// deleted.
	if op == syncer.Create {
		_, found, _ := c.serviceSyncer.GetResource(serviceName, endpointSlice.Namespace)
		if !found {
			logger.Infof("The service %q for EndpointSlice \"%s/%s\" does not exist - deleting it",
				serviceName, endpointSlice.Namespace, endpointSlice.Name)

			err := c.syncer.GetLocalFederator().Delete(ctx, endpointSlice)
			if apierrors.IsNotFound(err) {
				err = nil
			}

			if err != nil {
				logger.Errorf(err, "Error deleting EndpointSlice %s/%s", endpointSlice.Namespace, endpointSlice.Name)
			}

			return nil, err != nil
		}
	}

	return obj, false
}

func isLegacyEndpointSlice(endpointSlice *discovery.EndpointSlice) bool {
	// Any EndpointSlice's name prior to 0.16 was suffixed with the cluster ID.
	return strings.HasSuffix(endpointSlice.Name, "-"+endpointSlice.Labels[mcsv1a1.LabelSourceCluster])
}

func (c *EndpointSliceController) onRemoteEndpointSlice(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	targetNamespace := endpointSlice.GetObjectMeta().GetLabels()[constants.LabelSourceNamespace]

	if op != syncer.Delete {
		if err := c.namespaceValidator.CheckAllowed(targetNamespace); err != nil {
			logger.Warningf("Rejecting EndpointSlice from cluster %q: %v", endpointSlice.Labels[mcsv1a1.LabelSourceCluster], err)

			// Delete stale local ServiceImport if it exists
			deleteErr := c.localClient.Resource(endpointSliceGVR).Namespace(targetNamespace).Delete(
				context.TODO(), endpointSlice.Name, metav1.DeleteOptions{})
			if deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				logger.Errorf(deleteErr, "Error deleting rejected EndpointSlice %s/%s", targetNamespace, endpointSlice.Name)

				return nil, true
			}

			return nil, false
		}
	}

	endpointSlice.Namespace = targetNamespace

	return endpointSlice, false
}

func (c *EndpointSliceController) onLocalEndpointSliceSynced(obj runtime.Object, op syncer.Operation) bool {
	endpointSlice := obj.(*discovery.EndpointSlice)
	ctx := context.TODO()

	serviceName := endpointSlice.Labels[mcsv1a1.LabelServiceName]
	serviceNamespace := endpointSlice.Labels[constants.LabelSourceNamespace]

	logger.Infof("Local EndpointSlice \"%s/%s\" for service %q %sd on broker",
		endpointSlice.Namespace, endpointSlice.Name, serviceName, op)

	if isLegacyEndpointSlice(endpointSlice) {
		logger.Infof("EndpointSlice \"%s/%s\" is legacy - skipping it",
			endpointSlice.Namespace, endpointSlice.Name)

		return false
	}

	if op != syncer.Delete {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
			mcsv1a1.NewServiceExportCondition(mcsv1a1.ServiceExportConditionReady, metav1.ConditionTrue,
				mcsv1a1.ServiceExportReasonExported, "Service was successfully exported to the broker"))
	}

	return false
}
