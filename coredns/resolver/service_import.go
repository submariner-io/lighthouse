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
	"strconv"

	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/loadbalancer"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (i *Interface) PutServiceImport(serviceImport *mcsv1a1.ServiceImport) {
	logger.Infof("Put %#v", serviceImport)

	name, ok := getSourceName(serviceImport)
	if !ok {
		return
	}

	key := keyFunc(getSourceNamespace(serviceImport), name)

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

	if !svcInfo.isHeadless {
		clusterName := getSourceCluster(serviceImport)

		clusterInfo := svcInfo.ensureClusterInfo(clusterName)
		clusterInfo.weight = getServiceWeightFrom(serviceImport, i.clusterStatus.GetLocalClusterID())
		clusterInfo.endpointRecords = []DNSRecord{{
			IP:          serviceImport.Spec.IPs[0],
			Ports:       serviceImport.Spec.Ports,
			ClusterName: clusterName,
		}}

		svcInfo.resetLoadBalancing()
		svcInfo.mergePorts()

		return
	}
}

func (i *Interface) RemoveServiceImport(serviceImport *mcsv1a1.ServiceImport) {
	logger.Infof("Remove %#v", serviceImport)

	name, found := getSourceName(serviceImport)
	if !found {
		return
	}

	key := keyFunc(getSourceNamespace(serviceImport), name)

	i.mutex.Lock()
	defer i.mutex.Unlock()

	serviceInfo, found := i.serviceMap[key]
	if !found {
		return
	}

	for _, info := range serviceImport.Status.Clusters {
		delete(serviceInfo.clusters, info.Cluster)
	}

	if len(serviceInfo.clusters) == 0 {
		delete(i.serviceMap, key)
		return
	}

	if !serviceInfo.isHeadless {
		serviceInfo.resetLoadBalancing()
	}

	serviceInfo.mergePorts()
}

func getSourceName(from *mcsv1a1.ServiceImport) (string, bool) {
	name, ok := from.Labels[mcsv1a1.LabelServiceName]
	if ok {
		return name, true
	}

	name, ok = from.Annotations["origin-name"]
	return name, ok
}

func getSourceNamespace(from *mcsv1a1.ServiceImport) string {
	ns, ok := from.Labels[constants.LabelSourceNamespace]
	if ok {
		return ns
	}

	return from.Annotations["origin-namespace"]
}

func getSourceCluster(from *mcsv1a1.ServiceImport) string {
	c, ok := from.Labels[constants.MCSLabelSourceCluster]
	if ok {
		return c
	}

	return from.Labels["lighthouse.submariner.io/sourceCluster"]
}

func getServiceWeightFrom(si *mcsv1a1.ServiceImport, forClusterName string) int64 {
	weightKey := constants.LoadBalancerWeightAnnotationPrefix + "/" + forClusterName
	if val, ok := si.Annotations[weightKey]; ok {
		f, err := strconv.ParseInt(val, 0, 64)
		if err != nil {
			return f
		}

		logger.Errorf(err, "Error parsing the %q annotation from ServiceImport %q", weightKey, si.Name)
	}

	return 1 // Zero will cause no selection
}
