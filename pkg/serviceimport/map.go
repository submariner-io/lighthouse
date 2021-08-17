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
	"math"
	"strconv"
	"sync"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	loadbalance "github.com/submariner-io/lighthouse/pkg/loadbalance"
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
	weight float64
}

type serviceInfo struct {
	key        string
	records    map[string]*clusterInfo
	balancer   *loadbalance.SmoothWeightedRR
	isHeadless bool
}

func (si *serviceInfo) resetLoadBalancing() {
	si.balancer.RemoveAll()

	for _, info := range si.records {
		_ = si.balancer.Add(info, info.weight)
	}
}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

func (m *Map) selectIP(si *serviceInfo, name, namespace string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) *DNSRecord {
	queueLength := len(si.balancer.All())
	for i := 0; i < queueLength; i++ {
		info := si.balancer.Next().(*clusterInfo)
		if checkCluster(info.name) && checkEndpoint(name, namespace, info.name) {
			return info.record
		}
	}

	return nil
}

func (m *Map) GetIP(namespace, name, cluster, localCluster string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) (record *DNSRecord, found, isLocal bool) {
	m.RLock()
	defer m.RUnlock()

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

	// Fall back to Round-Robin if service is not presented in the local cluster
	record = m.selectIP(si, name, namespace, checkCluster, checkEndpoint)

	if record != nil {
		return record, true, false
	}

	return nil, true, false
}

func NewMap() *Map {
	return &Map{
		svcMap: make(map[string]*serviceInfo),
	}
}

func (m *Map) Put(serviceImport *mcsv1a1.ServiceImport) {
	if name, ok := serviceImport.Annotations["origin-name"]; ok {
		namespace := serviceImport.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)

		m.Lock()
		defer m.Unlock()

		remoteService, ok := m.svcMap[key]

		if !ok {
			remoteService = &serviceInfo{
				key:        key,
				records:    make(map[string]*clusterInfo),
				balancer:   loadbalance.NewSWRR(),
				isHeadless: serviceImport.Spec.Type == mcsv1a1.Headless,
			}
		}

		if serviceImport.Spec.Type == mcsv1a1.ClusterSetIP {
			record := &DNSRecord{
				IP:    serviceImport.Spec.IPs[0],
				Ports: serviceImport.Spec.Ports,
			}
			clusterName := serviceImport.GetLabels()[lhconstants.LabelSourceCluster]
			remoteService.records[clusterName] = &clusterInfo{
				name:   clusterName,
				record: record,
				weight: getServiceWeightFrom(serviceImport.Annotations),
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

		m.Lock()
		defer m.Unlock()

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

func getServiceWeightFrom(annotation map[string]string) float64 {
	if val, ok := annotation["remote-weight"]; ok {
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return f
		}
	}

	return math.SmallestNonzeroFloat64 // Zero will cause no selection
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
