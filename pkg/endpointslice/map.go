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

package endpointslice

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/serviceimport"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type endpointInfo struct {
	key         string
	clusterInfo map[string]*clusterInfo
}

type clusterInfo struct {
	hostRecords map[string][]serviceimport.DNSRecord
	recordList  []serviceimport.DNSRecord
}

type Map struct {
	epMap map[string]*endpointInfo
	mutex sync.RWMutex
}

func (m *Map) GetDNSRecords(hostname, cluster, namespace, name string, checkCluster func(string) bool) ([]serviceimport.DNSRecord, bool) {
	key := keyFunc(name, namespace)

	clusterInfos := func() map[string]*clusterInfo {
		m.mutex.RLock()
		defer m.mutex.RUnlock()

		result, ok := m.epMap[key]
		if !ok {
			return nil
		}

		return result.clusterInfo
	}()

	if clusterInfos == nil {
		return nil, false
	}

	switch {
	case cluster == "":
		records := make([]serviceimport.DNSRecord, 0)

		for clusterID, info := range clusterInfos {
			if checkCluster == nil || checkCluster(clusterID) {
				records = append(records, info.recordList...)
			}
		}

		return records, true
	case clusterInfos[cluster] == nil:
		return nil, false
	case hostname == "":
		return clusterInfos[cluster].recordList, true
	case clusterInfos[cluster].hostRecords == nil:
		return nil, false
	default:
		records, ok := clusterInfos[cluster].hostRecords[hostname]
		return records, ok
	}
}

func NewMap() *Map {
	return &Map{
		epMap: make(map[string]*endpointInfo),
	}
}

func (m *Map) Put(es *discovery.EndpointSlice) {
	key, ok := getKey(es)
	if !ok {
		klog.Warningf("Failed to get key labels from %#v", es.ObjectMeta)
		return
	}

	cluster, ok := es.Labels[constants.MCSLabelSourceCluster]

	// Remove this after 0.12 (this handles old map entries with pre-MCS labels)
	if !ok {
		cluster, ok = es.Labels[constants.LighthouseLabelSourceCluster]
	}

	if !ok {
		klog.Warningf("Cluster label missing on %#v", es.ObjectMeta)
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	epInfo, ok := m.epMap[key]
	if !ok {
		epInfo = &endpointInfo{
			key:         key,
			clusterInfo: make(map[string]*clusterInfo),
		}
	}

	epInfo.clusterInfo[cluster] = &clusterInfo{
		recordList:  make([]serviceimport.DNSRecord, 0),
		hostRecords: make(map[string][]serviceimport.DNSRecord),
	}

	mcsPorts := make([]mcsv1a1.ServicePort, len(es.Ports))

	for i, port := range es.Ports {
		mcsPort := mcsv1a1.ServicePort{
			Name:        *port.Name,
			Protocol:    *port.Protocol,
			AppProtocol: port.AppProtocol,
			Port:        *port.Port,
		}
		mcsPorts[i] = mcsPort
	}

	for _, endpoint := range es.Endpoints {
		var records []serviceimport.DNSRecord

		for _, address := range endpoint.Addresses {
			record := serviceimport.DNSRecord{
				IP:          address,
				Ports:       mcsPorts,
				ClusterName: cluster,
			}

			if endpoint.Hostname != nil {
				record.HostName = *endpoint.Hostname
			}

			records = append(records, record)
		}

		if endpoint.Hostname != nil {
			epInfo.clusterInfo[cluster].hostRecords[*endpoint.Hostname] = records
		}

		epInfo.clusterInfo[cluster].recordList = append(epInfo.clusterInfo[cluster].recordList, records...)
	}

	klog.V(log.DEBUG).Infof("Adding clusterInfo %#v for EndpointSlice %q in %q", epInfo.clusterInfo[cluster], es.Name, cluster)

	m.epMap[key] = epInfo
}

func (m *Map) Remove(es *discovery.EndpointSlice) {
	key, ok := getKey(es)
	if ok {
		cluster, ok := es.Labels[constants.MCSLabelSourceCluster]

		// Remove this after 0.12 (this handles old map entries with pre-MCS labels)
		if !ok {
			cluster, ok = es.Labels[constants.LighthouseLabelSourceCluster]
		}

		if !ok {
			return
		}

		m.mutex.Lock()
		defer m.mutex.Unlock()

		epInfo, ok := m.epMap[key]
		if !ok {
			return
		}

		klog.V(log.DEBUG).Infof("Adding endpointInfo %#v for %s in %s", epInfo.clusterInfo[cluster], es.Name, cluster)
		delete(epInfo.clusterInfo, cluster)
	}
}

func (m *Map) get(key string) *endpointInfo {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	endpointInfo := m.epMap[key]

	return endpointInfo
}

func getKey(es *discovery.EndpointSlice) (string, bool) {
	name, ok := es.Labels[constants.MCSLabelServiceName]

	if !ok {
		name, ok = es.Labels[constants.LighthouseLabelSourceName]
	}

	if !ok {
		return "", false
	}

	namespace, ok := es.Labels[constants.LabelSourceNamespace]

	if !ok {
		return "", false
	}

	return keyFunc(name, namespace), true
}

func keyFunc(name, namespace string) string {
	return name + "-" + namespace
}
