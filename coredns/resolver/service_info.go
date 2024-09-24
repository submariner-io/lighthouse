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
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (si *serviceInfo) resetLoadBalancing() {
	si.balancer.RemoveAll()

	for name, info := range si.clusters {
		err := si.balancer.Add(name, info.weight)
		if err != nil {
			logger.Error(err, "Error adding load balancer info")
		}
	}
}

func (si *serviceInfo) mergePorts() {
	si.ports = nil

	for _, info := range si.clusters {
		if si.ports == nil {
			si.ports = info.endpointRecords[0].Ports
		} else {
			si.ports = slices.Intersect(si.ports, info.endpointRecords[0].Ports, func(p mcsv1a1.ServicePort) string {
				return fmt.Sprintf("%s:%s:%d:%s", p.Name, p.Protocol, p.Port, ptr.Deref(p.AppProtocol, ""))
			})
		}
	}
}

func (si *serviceInfo) ensureClusterInfo(name string) *clusterInfo {
	info, ok := si.clusters[name]

	if !ok {
		info = &clusterInfo{
			endpointRecordsByHost: make(map[string][]DNSRecord),
			weight:                1,
		}

		si.clusters[name] = info
	}

	return info
}

func (si *serviceInfo) newRecordFrom(from *DNSRecord) *DNSRecord {
	r := *from
	r.Ports = si.ports

	return &r
}

func (si *serviceInfo) selectIP(checkCluster func(string) bool) *DNSRecord {
	queueLength := si.balancer.ItemCount()
	for i := 0; i < queueLength; i++ {
		clusterID := si.balancer.Next().(string)
		clusterInfo := si.clusters[clusterID]

		if checkCluster(clusterID) && clusterInfo.endpointsHealthy {
			return &clusterInfo.endpointRecords[0]
		}

		// Will Skip the cluster until a full "round" of the items is done
		si.balancer.Skip(clusterID)
	}

	return nil
}

func (si *serviceInfo) isHeadless() bool {
	return si.spec.Type == mcsv1a1.Headless
}
