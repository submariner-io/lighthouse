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
	"github.com/submariner-io/lighthouse/coredns/loadbalancer"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (i *Interface) PutServiceImport(serviceImport *mcsv1a1.ServiceImport) {
	if ignoreServiceImport(serviceImport) {
		return
	}

	key, isLegacy := getServiceImportKey(serviceImport)

	logger.Infof("Put ServiceImport %q", key)

	i.mutex.Lock()
	defer i.mutex.Unlock()

	svcInfo, found := i.serviceMap[key]

	if !found {
		svcInfo = &serviceInfo{
			clusters:   make(map[string]*clusterInfo),
			balancer:   loadbalancer.NewSmoothWeightedRR(),
			isHeadless: serviceImport.Spec.Type == mcsv1a1.Headless,
		}

		i.serviceMap[key] = svcInfo
	}

	svcInfo.isExported = true

	if svcInfo.isHeadless || !isLegacy {
		return
	}

	// This is a legacy pre-0.15 remote cluster ServiceImport - initialize the cluster info to maintain backwards compatibility
	// while roling upgrade is in progress.

	clusterName := serviceImport.Labels["lighthouse.submariner.io/sourceCluster"]

	clusterInfo := svcInfo.ensureClusterInfo(clusterName)
	clusterInfo.endpointRecords = []DNSRecord{{
		IP:          serviceImport.Spec.IPs[0],
		Ports:       serviceImport.Spec.Ports,
		ClusterName: clusterName,
	}}

	svcInfo.mergePorts()
	svcInfo.resetLoadBalancing()
}

func (i *Interface) RemoveServiceImport(serviceImport *mcsv1a1.ServiceImport) {
	if ignoreServiceImport(serviceImport) {
		return
	}

	key, isLegacy := getServiceImportKey(serviceImport)
	if isLegacy {
		return
	}

	logger.Infof("Remove ServiceImport %q", key)

	i.mutex.Lock()
	defer i.mutex.Unlock()

	svcInfo, found := i.serviceMap[key]
	if found {
		if len(svcInfo.clusters) == 0 {
			delete(i.serviceMap, key)
		} else {
			svcInfo.isExported = false
		}
	}
}

func getServiceImportKey(from *mcsv1a1.ServiceImport) (string, bool) {
	name, ok := from.Annotations["origin-name"]
	if ok {
		return keyFunc(from.Annotations["origin-namespace"], name), true
	}

	return keyFunc(from.Namespace, from.Name), false
}

func ignoreServiceImport(serviceImport *mcsv1a1.ServiceImport) bool {
	_, isLocal := serviceImport.Labels[mcsv1a1.LabelServiceName]
	_, isOnBroker := serviceImport.Annotations[mcsv1a1.LabelServiceName]

	return isLocal || isOnBroker
}
