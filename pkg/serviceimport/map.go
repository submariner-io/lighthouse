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
	"sync"
	"sync/atomic"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
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
	weight uint64
}

type serviceInfo struct {
	key           string
	records       map[string]*DNSRecord
	clustersQueue []clusterInfo
	rrCount       uint64
	isHeadless    bool
}

func (si *serviceInfo) buildClusterInfoQueue() {
	si.clustersQueue = make([]clusterInfo, 0)
	for cluster, record := range si.records {
		c := clusterInfo{name: cluster, record: record, weight: 0}
		si.clustersQueue = append(si.clustersQueue, c)
	}
}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

func (m *Map) selectIP(queue []clusterInfo, counter *uint64, name, namespace string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) *DNSRecord {
	queueLength := len(queue)
	for i := 0; i < queueLength; i++ {
		c := atomic.LoadUint64(counter)

		info := queue[c%uint64(queueLength)]

		atomic.AddUint64(counter, 1)

		if checkCluster(info.name) && checkEndpoint(name, namespace, info.name) {
			return info.record
		}
	}

	return nil
}

func (m *Map) GetIP(namespace, name, cluster, localCluster string, checkCluster func(string) bool,
	checkEndpoint func(string, string, string) bool) (record *DNSRecord, found, isLocal bool) {
	dnsRecords, queue, counter, isHeadless := func() (map[string]*DNSRecord, []clusterInfo, *uint64, bool) {
		m.RLock()
		defer m.RUnlock()

		si, ok := m.svcMap[keyFunc(namespace, name)]
		if !ok {
			return nil, nil, nil, false
		}

		return si.records, si.clustersQueue, &si.rrCount, si.isHeadless
	}()

	if dnsRecords == nil || isHeadless {
		return nil, false, false
	}

	// If a clusterID is specified, we supply it even if the service is not there
	if cluster != "" {
		record, found = dnsRecords[cluster]
		return record, found, cluster == localCluster
	}

	// If we are aware of the local cluster
	// And we found some accessible IP, we shall return it
	if localCluster != "" {
		record, found := dnsRecords[localCluster]

		if found && record != nil && checkEndpoint(name, namespace, localCluster) {
			return record, found, true
		}
	}

	// Fall back to Round-Robin if service is not presented in the local cluster
	record = m.selectIP(queue, counter, name, namespace, checkCluster, checkEndpoint)

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
				records:    make(map[string]*DNSRecord),
				rrCount:    0,
				isHeadless: serviceImport.Spec.Type == mcsv1a1.Headless,
			}
		}

		if serviceImport.Spec.Type == mcsv1a1.ClusterSetIP {
			record := &DNSRecord{
				IP:    serviceImport.Spec.IPs[0],
				Ports: serviceImport.Spec.Ports,
			}
			remoteService.records[serviceImport.GetLabels()[lhconstants.LabelSourceCluster]] = record
		}

		if !remoteService.isHeadless {
			remoteService.buildClusterInfoQueue()
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
			remoteService.buildClusterInfoQueue()
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
