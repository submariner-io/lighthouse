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
	"k8s.io/client-go/dynamic"
)

func New(clusterStatus ClusterStatus, client dynamic.Interface) *Interface {
	return &Interface{
		clusterStatus: clusterStatus,
		serviceMap:    make(map[string]*serviceInfo),
		client:        client,
	}
}

func (i *Interface) GetDNSRecords(namespace, name, clusterID, hostname string) (records []DNSRecord, isHeadless, found bool) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	serviceInfo, found := i.serviceMap[keyFunc(namespace, name)]
	if !found {
		return nil, false, false
	}

	if !serviceInfo.isHeadless {
		record, found := i.getClusterIPRecord(serviceInfo, clusterID)
		if record != nil {
			return []DNSRecord{*record}, false, true
		}

		return nil, false, found
	}

	records, found = i.getHeadlessRecords(serviceInfo, clusterID, hostname)
	return records, true, found
}

func (i *Interface) getClusterIPRecord(serviceInfo *serviceInfo, clusterID string) (*DNSRecord, bool) {
	// If a clusterID is specified, we supply it even if the service is not healthy.
	if clusterID != "" {
		clusterInfo, found := serviceInfo.clusters[clusterID]
		if !found {
			return nil, false
		}

		return &clusterInfo.endpointRecords[0], true
	}

	// If we are aware of the local cluster and we found some accessible IP, we shall return it.
	localClusterID := i.clusterStatus.GetLocalClusterID()
	if localClusterID != "" {
		clusterInfo, found := serviceInfo.clusters[localClusterID]
		if found && clusterInfo.endpointsHealthy {
			return serviceInfo.newRecordFrom(&clusterInfo.endpointRecords[0]), true
		}
	}

	// Fall back to selected load balancer (weighted/RR/etc) if service is not present in the local cluster
	record := serviceInfo.selectIP(i.clusterStatus.IsConnected)

	if record != nil {
		return serviceInfo.newRecordFrom(record), true
	}

	return nil, true
}

func (i *Interface) getHeadlessRecords(serviceInfo *serviceInfo, clusterID, hostname string) ([]DNSRecord, bool) {
	clusterInfo, clusterFound := serviceInfo.clusters[clusterID]

	switch {
	case clusterID == "":
		records := make([]DNSRecord, 0)

		for id, info := range serviceInfo.clusters {
			if i.clusterStatus.IsConnected(id) {
				records = append(records, info.endpointRecords...)
			}
		}

		return records, true
	case !clusterFound:
		return nil, false
	case hostname == "":
		return clusterInfo.endpointRecords, true
	default:
		records, found := clusterInfo.endpointRecordsByHost[hostname]
		return records, found
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
