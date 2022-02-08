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
package serviceimport

import (
	"strconv"
	"sync"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/loadbalancer"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type DNSRecord struct {
	IP          string
	Ports       []mcsv1a1.ServicePort
	HostName    string
	ClusterName string
}

type clusterInfo struct {
	record *DNSRecord
	name   string
	weight int64
}

type serviceInfo struct {
	key        string
	records    map[string]*clusterInfo
	balancer   loadbalancer.Interface
	isHeadless bool
}

func (si *serviceInfo) resetLoadBalancing() {
	si.balancer.RemoveAll()

	for _, info := range si.records {
		err := si.balancer.Add(info.name, info.weight)
		if err != nil {
			klog.Error(err)
		}
	}
}

type Map struct {
	svcMap         map[string]*serviceInfo
	localClusterID string
	mutex          sync.RWMutex
}

func (m *Map) selectIP(si *serviceInfo, name, namespace string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) *DNSRecord {
	queueLength := si.balancer.ItemCount()
	for i := 0; i < queueLength; i++ {
		selectedName := si.balancer.Next().(string)
		info := si.records[selectedName]

		if checkCluster(info.name) && checkEndpoint(name, namespace, info.name) {
			return info.record
		}

		// Will Skip the selected name until a full "round" of the items is done
		si.balancer.Skip(selectedName)
	}

	return nil
}

func (m *Map) GetIP(namespace, name, cluster, localCluster string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) (record *DNSRecord, found, isLocal bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	si, ok := m.svcMap[keyFunc(namespace, name)]
	if !ok || si.isHeadless {
		return nil, false, false
	}

	// If a clusterID is specified, we supply it even if the service is not there
	if cluster != "" {
		info, found := si.records[cluster]
		if !found {
			return nil, found, cluster == localCluster
		}

		return info.record, found, cluster == localCluster
	}

	// If we are aware of the local cluster
	// And we found some accessible IP, we shall return it
	if localCluster != "" {
		info, found := si.records[localCluster]
		if found && info != nil && checkEndpoint(name, namespace, localCluster) {
			return info.record, found, true
		}
	}

	// Fall back to selected load balancer (weighted/RR/etc) if service is not presented in the local cluster
	record = m.selectIP(si, name, namespace, checkCluster, checkEndpoint)

	if record != nil {
		return record, true, false
	}

	return nil, true, false
}

func NewMap(localClusterID string) *Map {
	return &Map{
		svcMap:         make(map[string]*serviceInfo),
		localClusterID: localClusterID,
	}
}

func (m *Map) Put(serviceImport *mcsv1a1.ServiceImport) {
	if name, ok := serviceImport.Annotations["origin-name"]; ok {
		namespace := serviceImport.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)

		m.mutex.Lock()
		defer m.mutex.Unlock()

		remoteService, ok := m.svcMap[key]

		if !ok {
			remoteService = &serviceInfo{
				key:        key,
				records:    make(map[string]*clusterInfo),
				balancer:   loadbalancer.NewSmoothWeightedRR(),
				isHeadless: serviceImport.Spec.Type == mcsv1a1.Headless,
			}
		}

		if serviceImport.Spec.Type == mcsv1a1.ClusterSetIP {
			clusterName := serviceImport.GetLabels()[lhconstants.LighthouseLabelSourceCluster]

			record := &DNSRecord{
				IP:          serviceImport.Spec.IPs[0],
				Ports:       serviceImport.Spec.Ports,
				ClusterName: clusterName,
			}

			remoteService.records[clusterName] = &clusterInfo{
				name:   clusterName,
				record: record,
				weight: getServiceWeightFrom(serviceImport, m.localClusterID),
			}
		}

		if !remoteService.isHeadless {
			remoteService.resetLoadBalancing()
		}

		m.svcMap[key] = remoteService
	}
}

func (m *Map) Remove(serviceImport *mcsv1a1.ServiceImport) {
	if name, ok := serviceImport.Annotations["origin-name"]; ok {
		namespace := serviceImport.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)

		m.mutex.Lock()
		defer m.mutex.Unlock()

		remoteService, ok := m.svcMap[key]
		if !ok {
			return
		}

		for _, info := range serviceImport.Status.Clusters {
			delete(remoteService.records, info.Cluster)
		}

		if len(remoteService.records) == 0 {
			delete(m.svcMap, key)
		} else if !remoteService.isHeadless {
			remoteService.resetLoadBalancing()
		}
	}
}

func getServiceWeightFrom(si *mcsv1a1.ServiceImport, forClusterName string) int64 {
	weightKey := lhconstants.LoadBalancerWeightAnnotationPrefix + "/" + forClusterName
	if val, ok := si.Annotations[weightKey]; ok {
		f, err := strconv.ParseInt(val, 0, 64)
		if err != nil {
			return f
		}

		klog.Errorf("Error: %v parsing the %q annotation from ServiceImport %q", err, weightKey, si.Name)
	}

	return 1 // Zero will cause no selection
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
