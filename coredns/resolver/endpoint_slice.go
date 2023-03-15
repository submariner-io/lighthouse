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

package resolver

import (
	"context"
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/lighthouse/coredns/constants"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (i *Interface) PutEndpointSlice(endpointSlice *discovery.EndpointSlice) bool {
	key, clusterID, ok := getKeyInfoFrom(endpointSlice)
	if !ok {
		return false
	}

	if ignoreEndpointSlice(endpointSlice) {
		return false
	}

	logger.Infof("Put EndpointSlice %q on cluster %q", key, clusterID)

	var localEndpointSliceErr error
	var localEndpointSlice *discovery.EndpointSlice

	localClusterID := i.clusterStatus.GetLocalClusterID()
	if localClusterID != "" && clusterID == localClusterID {
		// The EndpointSlice is from the local cluster. If globalnet is enabled, the local global endpoint IPs aren't
		// routable in the local cluster so we retrieve the K8s EndpointSlice and use those endpoints. Note that this
		// only applies to headless services.
		localEndpointSlice, localEndpointSliceErr = i.getLocalEndpointSlice(endpointSlice)
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()

	serviceInfo, found := i.serviceMap[key]
	if !found {
		// This means we haven't observed a ServiceImport yet for the service. Return true for the controller to re-queue it.
		logger.Infof("Service not found for EndpointSlice %q - requeuing", key)

		return true
	}

	if !serviceInfo.isHeadless {
		return i.putClusterIPEndpointSlice(key, clusterID, endpointSlice, serviceInfo)
	}

	if localEndpointSliceErr != nil {
		logger.Error(localEndpointSliceErr, "unable to retrieve local EndpointSlice - requeuing")

		return true
	}

	i.putHeadlessEndpointSlice(key, clusterID, endpointSlice, serviceInfo, localEndpointSlice)

	return false
}

func (i *Interface) putClusterIPEndpointSlice(key, clusterID string, endpointSlice *discovery.EndpointSlice, serviceInfo *serviceInfo) bool {
	_, found := endpointSlice.Labels[constants.LabelIsHeadless]
	if !found {
		// This is a legacy pre-0.15 EndpointSlice.
		clusterInfo, found := serviceInfo.clusters[clusterID]
		if !found {
			logger.Infof("Cluster %q not found for EndpointSlice %q - requeuing", clusterID, key)
			return true
		}

		// For a ClusterIPService we really only care if there are any backing endpoints.
		clusterInfo.endpointsHealthy = len(endpointSlice.Endpoints) > 0

		return false
	}

	if len(endpointSlice.Endpoints) == 0 {
		// This shouldn't happen - we expect the service IP endpoint to always be present.
		logger.Errorf(nil, "Missing service IP endpoint in EndpointSlice %q", key)

		return false
	}

	clusterInfo := serviceInfo.ensureClusterInfo(clusterID)
	clusterInfo.endpointRecords = []DNSRecord{{
		IP:          endpointSlice.Endpoints[0].Addresses[0],
		Ports:       mcsServicePortsFrom(endpointSlice.Ports),
		ClusterName: clusterID,
	}}

	clusterInfo.endpointsHealthy = endpointSlice.Endpoints[0].Conditions.Ready == nil || *endpointSlice.Endpoints[0].Conditions.Ready

	serviceInfo.mergePorts()
	serviceInfo.resetLoadBalancing()

	logger.Infof("Added DNSRecord with service IP %q for EndpointSlice %q on cluster %q, endpointsHealthy: %v, ports: %#v",
		clusterInfo.endpointRecords[0].IP, key, clusterID, clusterInfo.endpointsHealthy, clusterInfo.endpointRecords[0].Ports)

	return false
}

func (i *Interface) putHeadlessEndpointSlice(key, clusterID string, endpointSlice *discovery.EndpointSlice, serviceInfo *serviceInfo,
	localEndpointSlice *discovery.EndpointSlice) {
	clusterInfo := &clusterInfo{
		endpointRecordsByHost: make(map[string][]DNSRecord),
	}

	serviceInfo.clusters[clusterID] = clusterInfo

	mcsPorts := mcsServicePortsFrom(endpointSlice.Ports)

	for i := range endpointSlice.Endpoints {
		endpoint := &endpointSlice.Endpoints[i]

		// Skip if not ready. Note: we're treating nil as ready to be on the safe side as the EndpointConditions doc
		// states "In most cases consumers should interpret this unknown state (ie nil) as ready".
		if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
			continue
		}

		var records []DNSRecord

		addresses := endpoint.Addresses
		if localEndpointSlice != nil {
			for _, localEndpoint := range localEndpointSlice.Endpoints {
				if localEndpoint.NodeName != nil && endpoint.NodeName != nil && *localEndpoint.NodeName == *endpoint.NodeName &&
					(endpoint.Hostname == nil || localEndpoint.TargetRef.Name == *endpoint.Hostname) {
					addresses = localEndpoint.Addresses
				}
			}
		}

		for _, address := range addresses {

			record := DNSRecord{
				IP:          address,
				Ports:       mcsPorts,
				ClusterName: clusterID,
			}

			if endpoint.Hostname != nil {
				record.HostName = *endpoint.Hostname
			}

			records = append(records, record)
		}

		if endpoint.Hostname != nil {
			clusterInfo.endpointRecordsByHost[*endpoint.Hostname] = records
		}

		clusterInfo.endpointRecords = append(clusterInfo.endpointRecords, records...)
	}

	logger.V(log.DEBUG).Infof("Added records for headless EndpointSlice %q from cluster %q: %#v",
		key, clusterID, clusterInfo.endpointRecords)
}

func (i *Interface) getLocalEndpointSlice(from *discovery.EndpointSlice) (*discovery.EndpointSlice, error) {
	epsGVR := schema.GroupVersionResource{
		Group:    discovery.SchemeGroupVersion.Group,
		Version:  discovery.SchemeGroupVersion.Version,
		Resource: "endpointslices",
	}

	epSlices, err := i.client.Resource(epsGVR).Namespace(from.Labels[constants.LabelSourceNamespace]).List(context.TODO(),
		metav1.ListOptions{
			LabelSelector: labels.Set(map[string]string{
				constants.KubernetesServiceName: from.Labels[mcsv1a1.LabelServiceName],
			}).String(),
		})
	if err != nil {
		return nil, err
	}

	if len(epSlices.Items) == 0 {
		return nil, fmt.Errorf("local EndpointSlice not found for %s/%s", from.Labels[constants.LabelSourceNamespace],
			from.Labels[mcsv1a1.LabelServiceName])
	}

	epSlice := &discovery.EndpointSlice{}
	_ = runtime.DefaultUnstructuredConverter.FromUnstructured(epSlices.Items[0].Object, epSlice)

	return epSlice, nil
}

func (i *Interface) RemoveEndpointSlice(endpointSlice *discovery.EndpointSlice) {
	key, clusterID, ok := getKeyInfoFrom(endpointSlice)
	if !ok {
		return
	}

	if ignoreEndpointSlice(endpointSlice) {
		return
	}

	logger.Infof("Remove EndpointSlice %q on cluster %q", key, clusterID)

	i.mutex.Lock()
	defer i.mutex.Unlock()

	serviceInfo, found := i.serviceMap[key]
	if !found {
		return
	}

	delete(serviceInfo.clusters, clusterID)

	if !serviceInfo.isHeadless {
		serviceInfo.mergePorts()
		serviceInfo.resetLoadBalancing()
	}
}

func getKeyInfoFrom(es *discovery.EndpointSlice) (string, string, bool) {
	name, ok := es.Labels[mcsv1a1.LabelServiceName]
	if !ok {
		logger.Warningf("EndpointSlice missing label %q: %#v", mcsv1a1.LabelServiceName, es.ObjectMeta)
		return "", "", false
	}

	namespace, ok := es.Labels[constants.LabelSourceNamespace]
	if !ok {
		logger.Warningf("EndpointSlice missing label %q: %#v", constants.LabelSourceNamespace, es.ObjectMeta)
		return "", "", false
	}

	clusterID, ok := es.Labels[constants.MCSLabelSourceCluster]
	if !ok {
		logger.Warningf("EndpointSlice missing label %q: %#v", constants.MCSLabelSourceCluster, es.ObjectMeta)
		return "", "", false
	}

	return keyFunc(namespace, name), clusterID, true
}

func mcsServicePortsFrom(ports []discovery.EndpointPort) []mcsv1a1.ServicePort {
	mcsPorts := make([]mcsv1a1.ServicePort, len(ports))
	for i, port := range ports {
		mcsPorts[i] = mcsv1a1.ServicePort{
			Name:        *port.Name,
			Protocol:    *port.Protocol,
			AppProtocol: port.AppProtocol,
			Port:        *port.Port,
		}
	}

	return mcsPorts
}

func ignoreEndpointSlice(eps *discovery.EndpointSlice) bool {
	isOnBroker := eps.Namespace != eps.Labels[constants.LabelSourceNamespace]
	return isOnBroker
}
