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
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/client-go/dynamic"
	k8snet "k8s.io/utils/net"
)

func New(clusterStatus ClusterStatus, client dynamic.Interface) *Interface {
	return &Interface{
		clusterStatus: clusterStatus,
		serviceMap:    make(map[string]*serviceInfo),
		client:        client,
	}
}

func (i *Interface) GetDNSRecords(namespace, name, clusterID, hostname string, ipFamily k8snet.IPFamily) ([]DNSRecord, bool, bool) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	serviceInfo, found := i.serviceMap[keyFunc(namespace, name)]
	if !found {
		return nil, false, false
	}

	if serviceInfo.isHeadless() {
		records, found := i.getHeadlessRecords(serviceInfo, ipFamily, clusterID, hostname)

		return records, true, found
	}

	var record *DNSRecord

	if ipFamily == k8snet.IPv4 {
		record, found = i.getClusterIPRecord(serviceInfo, discovery.AddressTypeIPv4, clusterID)
	} else if ipFamily == k8snet.IPv6 {
		record, found = i.getClusterIPRecord(serviceInfo, discovery.AddressTypeIPv6, clusterID)
	} else {
		for _, addrType := range []discovery.AddressType{discovery.AddressTypeIPv4, discovery.AddressTypeIPv6} {
			record, found = i.getClusterIPRecord(serviceInfo, addrType, clusterID)
			if record != nil {
				break
			}
		}
	}

	if record != nil {
		return []DNSRecord{*record}, false, true
	}

	return nil, false, found
}

func (i *Interface) getClusterIPRecord(serviceInfo *serviceInfo, addrType discovery.AddressType, clusterID string) (*DNSRecord, bool) {
	ipFamilyInfo := serviceInfo.getIPFamilyInfo(addrType)

	// If a clusterID is specified, we supply it even if the service is not healthy.
	if clusterID != "" {
		clusterInfo, found := ipFamilyInfo.clusters[clusterID]
		if !found {
			return nil, false
		}

		return &clusterInfo.endpointRecords[0], true
	}

	if len(serviceInfo.spec.IPs) > 0 {
		return &DNSRecord{
			IP:    serviceInfo.spec.IPs[0],
			Ports: serviceInfo.spec.Ports,
		}, true
	}

	// If we are aware of the local cluster and we found some accessible IP, we shall return it.
	localClusterID := i.clusterStatus.GetLocalClusterID()
	if localClusterID != "" {
		clusterInfo, found := ipFamilyInfo.clusters[localClusterID]
		if found && clusterInfo.endpointsHealthy {
			return ipFamilyInfo.newRecordFrom(&clusterInfo.endpointRecords[0]), true
		}
	}

	// Fall back to selected load balancer (weighted/RR/etc) if service is not present in the local cluster
	record := ipFamilyInfo.selectIP(i.clusterStatus.IsConnected)

	if record != nil {
		return ipFamilyInfo.newRecordFrom(record), true
	}

	return nil, true
}

func (i *Interface) getHeadlessRecords(serviceInfo *serviceInfo, ipFamily k8snet.IPFamily, clusterID, hostname string) ([]DNSRecord, bool) {
	var (
		records []DNSRecord
		found   bool
	)

	if ipFamily == k8snet.IPv4 {
		records, found = i.getHeadlessRecordsForIPFamily(&serviceInfo.ipv4Info, clusterID, hostname)
	} else if ipFamily == k8snet.IPv6 {
		records, found = i.getHeadlessRecordsForIPFamily(&serviceInfo.ipv6Info, clusterID, hostname)
	} else {
		for _, ipFamilyInfo := range []*IPFamilyInfo{&serviceInfo.ipv4Info, &serviceInfo.ipv6Info} {
			r, f := i.getHeadlessRecordsForIPFamily(ipFamilyInfo, clusterID, hostname)
			records = append(records, r...)
			found = found || f
		}
	}

	return records, found
}

func (i *Interface) getHeadlessRecordsForIPFamily(ipFamilyInfo *IPFamilyInfo, clusterID, hostname string) ([]DNSRecord, bool) {
	clusterInfo, clusterFound := ipFamilyInfo.clusters[clusterID]

	switch {
	case clusterID == "":
		records := make([]DNSRecord, 0)

		for id, info := range ipFamilyInfo.clusters {
			if i.clusterStatus.IsConnected(id, ipFamilyInfo.getNetIPFamily()) {
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
