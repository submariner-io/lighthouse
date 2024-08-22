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
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/workqueue"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func newEndpointSliceController(spec *AgentSpecification, syncerConfig broker.SyncerConfig,
	serviceExportClient *ServiceExportClient, serviceSyncer syncer.Interface,
) (*EndpointSliceController, error) {
	c := &EndpointSliceController{
		clusterID:              spec.ClusterID,
		serviceExportClient:    serviceExportClient,
		serviceSyncer:          serviceSyncer,
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
			OnSuccessfulSyncFromBroker: func(obj runtime.Object, op syncer.Operation) bool {
				c.enqueueForConflictCheck(context.TODO(), obj.(*discovery.EndpointSlice), op)
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

	logger.V(log.DEBUG).Infof("Local EndpointSlice \"%s/%s\" for service %q %sd",
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
	return strings.HasSuffix(endpointSlice.Name, "-"+endpointSlice.Labels[constants.MCSLabelSourceCluster])
}

func (c *EndpointSliceController) onRemoteEndpointSlice(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {
	endpointSlice := obj.(*discovery.EndpointSlice)
	endpointSlice.Namespace = endpointSlice.GetObjectMeta().GetLabels()[constants.LabelSourceNamespace]

	return endpointSlice, false
}

func (c *EndpointSliceController) onLocalEndpointSliceSynced(obj runtime.Object, op syncer.Operation) bool {
	endpointSlice := obj.(*discovery.EndpointSlice)
	ctx := context.TODO()

	serviceName := endpointSlice.Labels[mcsv1a1.LabelServiceName]
	serviceNamespace := endpointSlice.Labels[constants.LabelSourceNamespace]

	logger.V(log.DEBUG).Infof("Local EndpointSlice \"%s/%s\" for service %q %sd on broker",
		endpointSlice.Namespace, endpointSlice.Name, serviceName, op)

	if isLegacyEndpointSlice(endpointSlice) {
		logger.Infof("EndpointSlice \"%s/%s\" is legacy - skipping it",
			endpointSlice.Namespace, endpointSlice.Name)

		return false
	}

	var err error

	if op == syncer.Delete {
		if c.hasNoRemainingEndpointSlices(endpointSlice) {
			err = c.serviceImportAggregator.updateOnDelete(ctx, serviceName, serviceNamespace)
		}
	} else {
		err = c.serviceImportAggregator.updateOnCreateOrUpdate(ctx, serviceName, serviceNamespace)
		if err != nil {
			c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
				newServiceExportCondition(constants.ServiceExportReady, corev1.ConditionFalse, ExportFailedReason,
					fmt.Sprintf("Unable to export: %v", err)))
		} else {
			c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
				newServiceExportCondition(constants.ServiceExportReady, corev1.ConditionTrue, "",
					"Service was successfully exported to the broker"))

			c.enqueueForConflictCheck(ctx, endpointSlice, op)
		}
	}

	if err != nil {
		logger.Errorf(err, "Error processing %sd EndpointSlice for service \"%s/%s\"", op, serviceNamespace, serviceName)
	}

	return err != nil
}

func (c *EndpointSliceController) hasNoRemainingEndpointSlices(endpointSlice *discovery.EndpointSlice) bool {
	if endpointSlice.Labels[constants.LabelIsHeadless] == strconv.FormatBool(true) {
		serviceNS := endpointSlice.Labels[constants.LabelSourceNamespace]

		list := c.syncer.ListLocalResourcesBySelector(&discovery.EndpointSlice{}, k8slabels.SelectorFromSet(map[string]string{
			constants.LabelSourceNamespace:  serviceNS,
			mcsv1a1.LabelServiceName:        endpointSlice.Labels[mcsv1a1.LabelServiceName],
			constants.MCSLabelSourceCluster: endpointSlice.Labels[constants.MCSLabelSourceCluster],
		}))

		count := 0

		for _, eps := range list {
			// Make sure we don't count ones in the broker namespace is co-located on the same cluster.
			if eps.(*discovery.EndpointSlice).Namespace == serviceNS {
				count++
			}
		}

		return count == 0
	}

	return true
}

func (c *EndpointSliceController) checkForConflicts(_, name, namespace string) (bool, error) {
	ctx := context.TODO()

	localServiceExport := c.serviceExportClient.getLocalInstance(name, namespace)
	if localServiceExport == nil {
		return false, nil
	}

	epsList := c.syncer.ListLocalResourcesBySelector(&discovery.EndpointSlice{}, k8slabels.SelectorFromSet(map[string]string{
		constants.LabelSourceNamespace: namespace,
		mcsv1a1.LabelServiceName:       name,
	}))

	var prevServicePorts []mcsv1a1.ServicePort
	var intersectedServicePorts []mcsv1a1.ServicePort
	clusterNames := make([]string, 0, len(epsList))
	conflict := false

	for _, o := range epsList {
		eps := o.(*discovery.EndpointSlice)

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
		c.serviceExportClient.UpdateStatusConditions(ctx, name, namespace, newServiceExportCondition(
			mcsv1a1.ServiceExportConflict, corev1.ConditionTrue, PortConflictReason,
			fmt.Sprintf("The service ports conflict between the constituent clusters %s. "+
				"The service will expose the intersection of all the ports: %s",
				fmt.Sprintf("[%s]", strings.Join(clusterNames, ", ")), servicePortsToString(intersectedServicePorts))))
	} else if FindServiceExportStatusCondition(localServiceExport.Status.Conditions, mcsv1a1.ServiceExportConflict) != nil {
		c.serviceExportClient.UpdateStatusConditions(ctx, name, namespace, newServiceExportCondition(
			mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, PortConflictReason, ""))
	}

	return false, nil
}

func (c *EndpointSliceController) enqueueForConflictCheck(ctx context.Context, eps *discovery.EndpointSlice, op syncer.Operation) {
	if eps.Labels[constants.LabelIsHeadless] != "false" {
		return
	}

	// Since the conflict checking works off of the local cache for efficiency, wait a little bit here for the local cache to be updated
	// with the latest state of the EndpointSlice.
	_ = wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 100*time.Millisecond, true,
		func(_ context.Context) (bool, error) {
			_, found, _ := c.syncer.GetLocalResource(eps.Name, eps.Namespace, eps)
			return (op == syncer.Delete && !found) || (op != syncer.Delete && found), nil
		},
	)

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
