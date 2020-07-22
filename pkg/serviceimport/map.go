package serviceimport

import (
	"sync"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
)

type serviceInfo struct {
	key         string
	clusterInfo map[string]string
	ipList      []string
}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

func (m *Map) GetIps(namespace, name string) ([]string, bool) {
	m.RLock()
	defer m.RUnlock()
	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		return val.ipList, len(val.ipList) > 0
	}
	return nil, false
}

func (m *Map) GetClusterInfo(namespace, name string) (map[string]string, bool) {
	m.RLock()
	defer m.RUnlock()
	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		return val.clusterInfo, len(val.ipList) > 0
	}
	return nil, false
}

func NewMap() *Map {
	return &Map{
		svcMap: make(map[string]*serviceInfo),
	}
}

func (m *Map) Put(serviceImport *lighthousev2a1.ServiceImport) {
	if name, ok := serviceImport.Annotations["origin-name"]; ok {
		namespace := serviceImport.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)
		m.Lock()
		defer m.Unlock()
		remoteService, ok := m.svcMap[key]
		if !ok {
			remoteService = &serviceInfo{
				key:         key,
				clusterInfo: make(map[string]string),
			}
		}
		for _, info := range serviceImport.Status.Clusters {
			remoteService.clusterInfo[info.Cluster] = info.IPs[0]
		}
		remoteService.ipList = make([]string, 0)
		for _, v := range remoteService.clusterInfo {
			remoteService.ipList = append(remoteService.ipList, v)
		}
		m.svcMap[key] = remoteService
	}
}

func (m *Map) Remove(serviceImport *lighthousev2a1.ServiceImport) {
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
			delete(remoteService.clusterInfo, info.Cluster)
		}
		if len(remoteService.clusterInfo) == 0 {
			delete(m.svcMap, key)
		} else {
			remoteService.ipList = make([]string, 0)
			for _, v := range remoteService.clusterInfo {
				remoteService.ipList = append(remoteService.ipList, v)
			}
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
