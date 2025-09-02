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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (c *ServiceImportController) createOrUpdateAggregate(ctx context.Context, obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key := localEndpointsControllerKey(localServiceImport)

	logger.V(log.DEBUG).Infof("Create/update aggregate for local ServiceImport %q", key)

	serviceName := serviceImportSourceName(localServiceImport)
	serviceNamespace := localServiceImport.Labels[constants.LabelSourceNamespace]

	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: brokerAggregatedServiceImportName(serviceName, serviceNamespace),
			Annotations: map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: serviceNamespace,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  localServiceImport.Spec.Type,
			Ports: []mcsv1a1.ServicePort{},
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: c.clusterID,
				},
			},
		},
	}

	typeConflict := false
	clusterSetIP := ""

	useClusterSetIP := c.determineUseClusterSetIP(localServiceImport)

	// Create the aggregated ServiceImport on the broker or update the existing instance with our local service info.
	result, newAggregate, err := util.CreateOrUpdateWithOptions(ctx, util.CreateOrUpdateOptions[*unstructured.Unstructured]{
		Client: resource.ForDynamic(c.brokerServiceImportClient()),
		Obj:    c.converter.toUnstructured(aggregate),
		MutateOnUpdate: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			existing := c.converter.toServiceImport(obj)

			if localServiceImport.Spec.Type != existing.Spec.Type {
				typeConflict = true
				c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
					mcsv1a1.NewServiceExportCondition(mcsv1a1.ServiceExportConditionReady,
						metav1.ConditionFalse, mcsv1a1.ServiceExportReasonFailed, "Unable to export due to an irresolvable conflict"))
			} else {
				if existing.Annotations == nil {
					existing.Annotations = map[string]string{}
				}

				if _, found := existing.Annotations[constants.UseClustersetIP]; !found {
					// This will happen on migration from pre-clusterset IP version
					existing.Annotations[constants.UseClustersetIP] = strconv.FormatBool(false)
				}

				var added bool

				existing.Status.Clusters, added = slices.AppendIfNotPresent(existing.Status.Clusters,
					mcsv1a1.ClusterStatus{Cluster: c.clusterID}, clusterStatusKey)

				if added {
					logger.V(log.DEBUG).Infof("Added cluster name %q to aggregated ServiceImport %q. New status: %#v",
						c.clusterID, existing.Name, existing.Status.Clusters)
				}
			}

			return c.converter.toUnstructured(existing), nil
		},
		MutateOnCreate: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			si := c.converter.toServiceImport(obj)

			if si.Spec.Type != mcsv1a1.ClusterSetIP {
				return obj, nil
			}

			var err error

			if useClusterSetIP {
				clusterSetIP, err = c.allocateClusterSetIPIfNeeded(clusterSetIP)

				si.Spec.IPs = []string{clusterSetIP}
				si.Annotations[constants.ClustersetIPAllocatedBy] = c.clusterID
			}

			si.Annotations[constants.UseClustersetIP] = strconv.FormatBool(useClusterSetIP)

			return c.converter.toUnstructured(si), err
		},
	})
	if err == nil && !typeConflict {
		err = c.startEndpointsController(ctx, localServiceImport)
	}

	if err != nil {
		c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace,
			mcsv1a1.NewServiceExportCondition(mcsv1a1.ServiceExportConditionReady,
				metav1.ConditionFalse, mcsv1a1.ServiceExportReasonFailed, fmt.Sprintf("Unable to export: %v", err)))

		if clusterSetIP != "" {
			_ = c.clustersetIPPool.Release(clusterSetIP)
		}
	}

	if result == util.OperationResultCreated {
		logger.V(log.DEBUG).Infof("Created aggregated ServiceImport %s", resource.ToJSON(newAggregate))
	}

	return err
}

func (c *ServiceImportController) updateAggregateOnDelete(ctx context.Context, obj runtime.Object) error {
	localServiceImport := c.converter.toServiceImport(obj)
	key := localEndpointsControllerKey(localServiceImport)

	logger.V(log.DEBUG).Infof("Update aggregate on delete of local ServiceImport %q", key)

	err := c.stopEndpointsController(ctx, key)
	if err != nil {
		return err
	}

	return c.updateAggregate(ctx, serviceImportSourceName(localServiceImport),
		localServiceImport.Labels[constants.LabelSourceNamespace], func(existing *mcsv1a1.ServiceImport) error {
			var removed bool

			existing.Status.Clusters, removed = slices.Remove(existing.Status.Clusters, mcsv1a1.ClusterStatus{Cluster: c.clusterID},
				clusterStatusKey)
			if !removed {
				return nil
			}

			logger.V(log.DEBUG).Infof("Removed cluster name %q from aggregated ServiceImport %q. New status: %#v",
				c.clusterID, existing.Name, existing.Status.Clusters)

			return nil
		})
}

func (c *ServiceImportController) updateAggregate(ctx context.Context, name, namespace string, mutate func(*mcsv1a1.ServiceImport) error,
) error {
	aggregate := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: brokerAggregatedServiceImportName(name, namespace),
		},
	}

	//nolint:wrapcheck // Let the caller wrap it
	return util.Update(ctx, resource.ForDynamic(c.brokerServiceImportClient()),
		c.converter.toUnstructured(aggregate),
		func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			existing := c.converter.toServiceImport(obj)

			err := mutate(existing)
			if err != nil {
				return nil, err
			}

			if len(existing.Status.Clusters) == 0 {
				logger.V(log.DEBUG).Infof("Deleting aggregated ServiceImport %q", existing.Name)

				err := c.brokerServiceImportClient().Delete(ctx, existing.Name, metav1.DeleteOptions{
					Preconditions: &metav1.Preconditions{
						ResourceVersion: ptr.To(existing.ResourceVersion),
					},
				})
				if apierrors.IsNotFound(err) {
					err = nil
				}

				return obj, errors.Wrapf(err, "error deleting aggregated ServiceImport %q", existing.Name)
			}

			return c.converter.toUnstructured(existing), nil
		})
}

func (c *ServiceImportController) reconcileRemoteAggregatedServiceImports() {
	c.localSyncer.Reconcile(func() []runtime.Object {
		siList := c.remoteSyncer.ListResources()
		retList := make([]runtime.Object, 0, len(siList))

		for i := range siList {
			si := c.converter.toServiceImport(siList[i])

			serviceName, ok := si.Annotations[mcsv1a1.LabelServiceName]
			if !ok {
				// This is not an aggregated ServiceImport.
				continue
			}

			if slices.IndexOf(si.Status.Clusters, c.clusterID, clusterStatusKey) < 0 {
				continue
			}

			si.Name = serviceName + "-" + si.Annotations[constants.LabelSourceNamespace] + "-" + c.clusterID
			si.Namespace = c.localNamespace
			si.Labels = map[string]string{
				mcsv1a1.LabelServiceName:       serviceName,
				constants.LabelSourceNamespace: si.Annotations[constants.LabelSourceNamespace],
			}

			retList = append(retList, si)
		}

		return retList
	})
}

func (c *ServiceImportController) reconcileLocalAggregatedServiceImports() {
	c.remoteSyncer.Reconcile(func() []runtime.Object {
		siList, err := c.localClient.Resource(serviceImportGVR).Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			logger.Error(err, "Error listing ServiceImports")
			return nil
		}

		retList := make([]runtime.Object, 0, len(siList.Items))

		for i := range siList.Items {
			si := c.converter.toServiceImport(&siList.Items[i])

			if serviceImportSourceName(si) != "" || si.Annotations[mcsv1a1.LabelServiceName] != "" {
				// This is not a local aggregated ServiceImport.
				continue
			}

			si.Annotations = map[string]string{
				mcsv1a1.LabelServiceName:       si.Name,
				constants.LabelSourceNamespace: si.Namespace,
			}

			si.Name = fmt.Sprintf("%s-%s", si.Name, si.Namespace)
			si.Namespace = c.brokerNamespace

			retList = append(retList, si)
		}

		return retList
	})
}

func (c *ServiceImportController) brokerServiceImportClient() dynamic.ResourceInterface {
	return c.brokerClient.Resource(serviceImportGVR).Namespace(c.brokerNamespace)
}

func brokerAggregatedServiceImportName(serviceName, serviceNamespace string) string {
	return fmt.Sprintf("%s-%s", serviceName, serviceNamespace)
}

func clusterStatusKey(c mcsv1a1.ClusterStatus) string {
	return c.Cluster
}
