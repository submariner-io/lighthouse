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
	"fmt"

	"github.com/submariner-io/admiral/pkg/slices"
	discovery "k8s.io/api/discovery/v1"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (i *IPFamilyInfo) resetLoadBalancing() {
	i.balancer.RemoveAll()

	for name, info := range i.clusters {
		err := i.balancer.Add(name, info.weight)
		if err != nil {
			logger.Error(err, "Error adding load balancer info")
		}
	}
}

func (i *IPFamilyInfo) mergePorts() {
	i.ports = nil

	for _, info := range i.clusters {
		if i.ports == nil {
			i.ports = info.endpointRecords[0].Ports
		} else {
			i.ports = slices.Intersect(i.ports, info.endpointRecords[0].Ports, func(p mcsv1a1.ServicePort) string {
				return fmt.Sprintf("%s:%s:%d:%s", p.Name, p.Protocol, p.Port, ptr.Deref(p.AppProtocol, ""))
			})
		}
	}
}

func (i *IPFamilyInfo) ensureClusterInfo(name string) *clusterInfo {
	info, ok := i.clusters[name]

	if !ok {
		info = &clusterInfo{
			endpointRecordsByHost: make(map[string][]DNSRecord),
			weight:                1,
		}

		i.clusters[name] = info
	}

	return info
}

func (i *IPFamilyInfo) newRecordFrom(from *DNSRecord) *DNSRecord {
	r := *from
	r.Ports = i.ports

	return &r
}

func (i *IPFamilyInfo) selectIP(checkCluster func(string, k8snet.IPFamily) bool) *DNSRecord {
	queueLength := i.balancer.ItemCount()
	for range queueLength {
		clusterID := i.balancer.Next().(string)
		clusterInfo := i.clusters[clusterID]

		if checkCluster(clusterID, i.getNetIPFamily()) && clusterInfo.endpointsHealthy {
			return &clusterInfo.endpointRecords[0]
		}

		// Will Skip the cluster until a full "round" of the items is done
		i.balancer.Skip(clusterID)
	}

	return nil
}

func (i *IPFamilyInfo) getNetIPFamily() k8snet.IPFamily {
	if i.addrType == discovery.AddressTypeIPv6 {
		return k8snet.IPv6
	}

	return k8snet.IPv4
}
