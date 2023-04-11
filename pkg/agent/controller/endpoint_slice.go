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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/workqueue"
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
		clusterID:              spec.ClusterID,
		serviceExportClient:    serviceExportClient,
		conflictCheckWorkQueue: workqueue.New("ConflictChecker"),
	}

	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			LocalSourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
				discovery.LabelManagedBy: constants.LabelValueManagedBy,
			}).String(),
			LocalResourceType:        &discovery.EndpointSlice{},
			TransformLocalToBroker:   c.onLocalEndpointSlice,
			OnSuccessfulSyncToBroker: c.onLocalEndpointSliceSynced,
			BrokerResourceType:       &discovery.EndpointSlice{},
			TransformBrokerToLocal:   c.onRemoteEndpointSlice,
			OnSuccessfulSyncFromBroker: func(obj runtime.Object, _ syncer.Operation) bool {
				c.enqueueForConflictCheck(obj.(*discovery.EndpointSlice))
				return false
			},
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

	c.conflictCheckWorkQueue.Run(stopCh, c.checkForConflicts)

	go func() {
		<-stopCh
		c.conflictCheckWorkQueue.ShutDown()
	}()

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

			c.enqueueForConflictCheck(endpointSlice)
		}
	}

	if err != nil {
		logger.Errorf(err, "Error processing %sd EndpointSlice for service \"%s/%s\"", op, serviceNamespace, serviceName)
	}

	return err != nil
}

func (c *EndpointSliceController) checkForConflicts(key, name, namespace string) (bool, error) {
	epsList, err := c.syncer.GetLocalClient().Resource(endpointSliceGVR).Namespace(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: k8slabels.SelectorFromSet(map[string]string{
			discovery.LabelManagedBy: constants.LabelValueManagedBy,
			mcsv1a1.LabelServiceName: name,
		}).String(),
	})
	if err != nil {
		return true, errors.Wrapf(err, "error during conflict check for %q", key)
	}

	var prevServicePorts []mcsv1a1.ServicePort
	var intersectedServicePorts []mcsv1a1.ServicePort
	clusterNames := make([]string, 0, len(epsList.Items))
	conflict := false

	for i := range epsList.Items {
		eps := c.serviceExportClient.toEndpointSlice(&epsList.Items[i])

		servicePorts := c.serviceExportClient.toServicePorts(eps.Ports)
		if prevServicePorts == nil {
			prevServicePorts = servicePorts
			intersectedServicePorts = servicePorts
		} else if !slices.Equivalent(prevServicePorts, servicePorts, servicePortKey) {
			conflict = true
		}

		intersectedServicePorts = slices.Intersect(intersectedServicePorts, servicePorts, servicePortKey)

		clusterNames = append(clusterNames, eps.Labels[constants.MCSLabelSourceCluster])
	}

	if conflict {
		c.serviceExportClient.updateStatusConditions(name, namespace, newServiceExportCondition(
			mcsv1a1.ServiceExportConflict, corev1.ConditionTrue, portConflictReason,
			fmt.Sprintf("The service ports conflict between the constituent clusters %s. "+
				"The service will expose the intersection of all the ports: %s",
				fmt.Sprintf("[%s]", strings.Join(clusterNames, ", ")), servicePortsToString(intersectedServicePorts))))
	} else {
		c.serviceExportClient.removeStatusCondition(name, namespace, mcsv1a1.ServiceExportConflict, portConflictReason)
	}

	return false, nil
}

func (c *EndpointSliceController) enqueueForConflictCheck(eps *discovery.EndpointSlice) {
	if eps.Labels[constants.LabelIsHeadless] != "false" {
		return
	}

	c.conflictCheckWorkQueue.Enqueue(&discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eps.Labels[mcsv1a1.LabelServiceName],
			Namespace: eps.Labels[constants.LabelSourceNamespace],
		},
	})
}

func servicePortsToString(p []mcsv1a1.ServicePort) string {
	s := make([]string, len(p))
	for i := range p {
		s[i] = fmt.Sprintf("[name: %s, protocol: %s, port: %v]", p[i].Name, p[i].Protocol, p[i].Port)
	}

	return strings.Join(s, ", ")
}
