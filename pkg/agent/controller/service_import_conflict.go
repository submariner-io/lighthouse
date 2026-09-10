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
	"math"
	"reflect"
	goslices "slices"
	"strings"

	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"k8s.io/utils/set"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

func (c *ServiceImportController) checkForConflicts(ctx context.Context, aggregatedServiceImport *mcsv1b1.ServiceImport,
) *mcsv1b1.ServiceImport {
	serviceName := aggregatedServiceImport.Annotations[mcsv1b1.LabelServiceName]
	serviceNamespace := aggregatedServiceImport.Annotations[constants.LabelSourceNamespace]

	siList := c.remoteSyncer.ListResourcesBySelector(k8slabels.SelectorFromSet(map[string]string{
		mcsv1b1.LabelServiceName:       serviceName,
		constants.LabelSourceNamespace: serviceNamespace,
	}))

	if len(siList) == 0 {
		return nil
	}

	sortServiceImportsByTimestamp(siList, aggregatedServiceImport.Spec.Type)

	precedentServiceImport := siList[0].(*mcsv1b1.ServiceImport)
	intersectionOfServicePorts := precedentServiceImport.Spec.Ports
	unionOfServicePorts := precedentServiceImport.Spec.Ports
	unionOfIPFamilies := precedentServiceImport.Spec.IPFamilies

	exportingClusters := set.New[string](precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster])
	conflictingServiceTypeClusters := set.New[string]()
	portConflict := false
	conflictingSessionAffinityClusters := set.New[string]()
	conflictingSessionAffinityConfigClusters := set.New[string]()
	conflictingTrafficDistributionClusters := set.New[string]()
	conflictingInternalTrafficPolicyClusters := set.New[string]()
	conflictingClusterSetIPEnablementClusters := set.New[string]()
	ipFamilyConflict := false

	for i := 1; i < len(siList); i++ {
		serviceImport := siList[i].(*mcsv1b1.ServiceImport)

		exportingClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])

		// If the service type differs then we don't actually export it so skip the rest of the checking.
		if serviceImport.Spec.Type != precedentServiceImport.Spec.Type {
			conflictingServiceTypeClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
			continue
		}

		if serviceImport.Spec.SessionAffinity != precedentServiceImport.Spec.SessionAffinity {
			conflictingSessionAffinityClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
		}

		if !reflect.DeepEqual(serviceImport.Spec.SessionAffinityConfig, precedentServiceImport.Spec.SessionAffinityConfig) {
			conflictingSessionAffinityConfigClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
		}

		if normalizeTrafficDistribution(serviceImport.Spec.TrafficDistribution) !=
			normalizeTrafficDistribution(precedentServiceImport.Spec.TrafficDistribution) {
			conflictingTrafficDistributionClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
		}

		if !ptr.Equal(serviceImport.Spec.InternalTrafficPolicy, precedentServiceImport.Spec.InternalTrafficPolicy) {
			conflictingInternalTrafficPolicyClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
		}

		if serviceImport.Annotations[constants.UseClustersetIP] != precedentServiceImport.Annotations[constants.UseClustersetIP] {
			conflictingClusterSetIPEnablementClusters.Insert(serviceImport.Labels[mcsv1b1.LabelSourceCluster])
		}

		unionOfServicePorts = slices.Union(unionOfServicePorts, serviceImport.Spec.Ports, func(p mcsv1b1.ServicePort) string {
			return p.Name
		})

		intersectionOfServicePorts = slices.Intersect(intersectionOfServicePorts, serviceImport.Spec.Ports, servicePortKey)

		portConflict = portConflict || !slices.Equivalent(precedentServiceImport.Spec.Ports, serviceImport.Spec.Ports, servicePortKey)

		unionOfIPFamilies = slices.Union(unionOfIPFamilies, serviceImport.Spec.IPFamilies, func(f corev1.IPFamily) string {
			return string(f)
		})

		ipFamilyConflict = ipFamilyConflict || !goslices.Equal(serviceImport.Spec.IPFamilies, precedentServiceImport.Spec.IPFamilies)
	}

	var conditions []metav1.Condition

	toStringClusterNames := func(names set.Set[string]) string {
		return fmt.Sprintf("[%s]", strings.Join(names.UnsortedList(), ", "))
	}

	addConflictCondition := func(conflict bool, reason mcsv1b1.ServiceExportConditionReason, message func() string) {
		if conflict {
			conditions = append(conditions, mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionConflict, metav1.ConditionTrue,
				reason, message()))
		} else if c.serviceExportClient.hasCondition(serviceName, serviceNamespace, mcsv1b1.ServiceExportConditionConflict, reason) {
			conditions = append(conditions, mcsv1b1.NewServiceExportCondition(
				mcsv1b1.ServiceExportConditionConflict, metav1.ConditionFalse, reason, ""))
		}
	}

	addConflictCondition(len(conflictingServiceTypeClusters) > 0, mcsv1b1.ServiceExportReasonTypeConflict, func() string {
		return fmt.Sprintf("The service type conflicts between the constituent clusters %s. "+
			"Using the setting %q determined by the first exporting cluster %q (clusters %s disagree).",
			toStringClusterNames(exportingClusters), aggregatedServiceImport.Spec.Type,
			aggregatedServiceImport.Status.Clusters[0].Cluster, toStringClusterNames(conflictingServiceTypeClusters))
	})

	addConflictCondition(portConflict, mcsv1b1.ServiceExportReasonPortConflict, func() string {
		exposedOp := "intersection"
		exposedPorts := intersectionOfServicePorts

		if len(aggregatedServiceImport.Spec.IPs) > 0 {
			exposedPorts = aggregatedServiceImport.Spec.Ports
			exposedOp = "union"
		}

		return fmt.Sprintf("The service ports conflict between the constituent clusters %s. "+
			"The service will expose the %s of all the ports: %s.", toStringClusterNames(exportingClusters), exposedOp,
			servicePortsToString(exposedPorts))
	})

	addConflictCondition(len(conflictingSessionAffinityClusters) > 0, mcsv1b1.ServiceExportReasonSessionAffinityConflict, func() string {
		return fmt.Sprintf("The service SessionAffinity conflicts between the constituent clusters %s. "+
			"Using SessionAffinity %q from the oldest exporting service in cluster %q (clusters %s disagree).",
			toStringClusterNames(exportingClusters), precedentServiceImport.Spec.SessionAffinity,
			precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster], toStringClusterNames(conflictingSessionAffinityClusters))
	})

	addConflictCondition(len(conflictingSessionAffinityConfigClusters) > 0, mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict,
		func() string {
			return fmt.Sprintf("The service SessionAffinityConfig conflicts between the constituent clusters %s. "+
				"Using SessionAffinityConfig %q from the oldest exporting service in cluster %q (clusters %s disagree).",
				toStringClusterNames(exportingClusters), toSessionAffinityConfigString(precedentServiceImport.Spec.SessionAffinityConfig),
				precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster], toStringClusterNames(conflictingSessionAffinityConfigClusters))
		})

	addConflictCondition(len(conflictingTrafficDistributionClusters) > 0, mcsv1b1.ServiceExportReasonTrafficDistributionConflict,
		func() string {
			return fmt.Sprintf("The service TrafficDistribution conflicts between the constituent clusters %s. "+
				"Using TrafficDistribution %q from the oldest exporting service in cluster %q (clusters %s disagree).",
				toStringClusterNames(exportingClusters), ptr.Deref(precedentServiceImport.Spec.TrafficDistribution, ""),
				precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster], toStringClusterNames(conflictingTrafficDistributionClusters))
		})

	addConflictCondition(len(conflictingInternalTrafficPolicyClusters) > 0, mcsv1b1.ServiceExportReasonInternalTrafficPolicyConflict,
		func() string {
			return fmt.Sprintf("The service InternalTrafficPolicy conflicts between the constituent clusters %s. "+
				"Using InternalTrafficPolicy %q from the oldest exporting service in cluster %q (clusters %s disagree).",
				toStringClusterNames(exportingClusters), ptr.Deref(precedentServiceImport.Spec.InternalTrafficPolicy, ""),
				precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster], toStringClusterNames(conflictingInternalTrafficPolicyClusters))
		})

	addConflictCondition(len(conflictingClusterSetIPEnablementClusters) > 0, ServiceExportReasonClusterSetIPEnablementConflict,
		func() string {
			clusterName := aggregatedServiceImport.Annotations[constants.ClustersetIPAllocatedBy]
			if clusterName == "" {
				clusterName = precedentServiceImport.Labels[mcsv1b1.LabelSourceCluster]
			}

			return fmt.Sprintf("The service clusterset IP enablement setting conflicts between the constituent clusters %s. "+
				"Using the setting %q determined by the first exporting cluster %q (clusters %s disagree).",
				toStringClusterNames(exportingClusters), aggregatedServiceImport.Annotations[constants.UseClustersetIP], clusterName,
				toStringClusterNames(conflictingClusterSetIPEnablementClusters))
		})

	addConflictCondition(ipFamilyConflict, mcsv1b1.ServiceExportReasonIPFamilyConflict, func() string {
		return fmt.Sprintf("The service IP families conflict between the constituent clusters %s. "+
			"The service will expose the union of all backends, but network traffic may only reach backends in a subset of clusters "+
			"depending on the client's IP family capabilities.",
			toStringClusterNames(exportingClusters))
	})

	c.serviceExportClient.UpdateStatusConditions(ctx, serviceName, serviceNamespace, conditions...)

	precedentServiceImport.Spec.Ports = unionOfServicePorts
	precedentServiceImport.Spec.IPFamilies = unionOfIPFamilies

	return precedentServiceImport
}

func servicePortKey(p mcsv1b1.ServicePort) string {
	return fmt.Sprintf("%s:%s:%d:%s", p.Name, p.Protocol, p.Port, ptr.Deref(p.AppProtocol, ""))
}

func servicePortsToString(p []mcsv1b1.ServicePort) string {
	s := make([]string, len(p))
	for i := range p {
		s[i] = fmt.Sprintf("[name: %s, protocol: %s, port: %v, appProtocol: %q]", p[i].Name, p[i].Protocol, p[i].Port,
			ptr.Deref(p[i].AppProtocol, ""))
	}

	return strings.Join(s, ", ")
}

func toSessionAffinityConfigString(c *corev1.SessionAffinityConfig) string {
	if c != nil && c.ClientIP != nil && c.ClientIP.TimeoutSeconds != nil {
		return fmt.Sprintf("ClientIP TimeoutSeconds: %d", *c.ClientIP.TimeoutSeconds)
	}

	return "none"
}

func getTimestamp(si *mcsv1b1.ServiceImport) int64 {
	if si.CreationTimestamp.IsZero() {
		return math.MaxInt64
	}

	return si.CreationTimestamp.UnixNano()
}

func sortServiceImportsByTimestamp(siList []runtime.Object, aggregatedType mcsv1b1.ServiceImportType) {
	goslices.SortFunc(siList, func(a, b runtime.Object) int {
		siA := a.(*mcsv1b1.ServiceImport)
		siB := b.(*mcsv1b1.ServiceImport)

		// Don't allow an unexported cluster's service to become precedent.
		if siA.Spec.Type != aggregatedType {
			return 1
		}

		if siB.Spec.Type != aggregatedType {
			return -1
		}

		tsA := getTimestamp(siA)
		tsB := getTimestamp(siB)

		if tsA < tsB {
			return -1
		} else if tsA > tsB {
			return 1
		}

		return strings.Compare(siA.Labels[mcsv1b1.LabelSourceCluster], siB.Labels[mcsv1b1.LabelSourceCluster])
	})
}

func normalizeTrafficDistribution(v *string) string {
	td := ptr.Deref(v, "")
	if td == corev1.ServiceTrafficDistributionPreferClose {
		td = corev1.ServiceTrafficDistributionPreferSameZone
	}

	return td
}
