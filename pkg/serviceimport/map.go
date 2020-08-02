package serviceimport

import (
	"sync"
	"sync/atomic"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
)

type clusterInfo struct {
	Ip				string
	Name			string
	Weight		uint64
}

type serviceInfo struct {
	key         string
	clusterInfo map[string]string
	ipList      []string
	queue 			[]clusterInfo // clusters queue [each could have an 1 vertual ip or an ip pool of nodes (headless)]
	rrCount			uint64
}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

func (m *Map) GetQueue(namespace, name string) ([]clusterInfo, bool) {
	m.RLock()
	defer m.RUnlock()
	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		return val.queue, len(val.queue) > 0
	}
	return nil, false
}

func (m *Map) GetAndIncRRCounter(namespace, name string) (uint64, bool) {
	m.RLock()
	defer m.RUnlock()
	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		atomic.AddUint64(&val.rrCount, 1)
		return val.rrCount, true
	}
	return 0, false
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
				rrCount:	0,
			}
		}

		for _, info := range serviceImport.Status.Clusters {
			remoteService.clusterInfo[info.Cluster] = info.IPs[0]
		}

		remoteService.queue = make([]clusterInfo, 0)
		remoteService.ipList = make([]string, 0)
		for k, v := range remoteService.clusterInfo {
			c := clusterInfo{ Name: k, Ip: v, Weight: 0, }
			remoteService.queue = append(remoteService.queue, c)
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
			remoteService.queue = make([]clusterInfo, 0)
			remoteService.ipList = make([]string, 0)
			for k, v := range remoteService.clusterInfo {
				c := clusterInfo{ Name: k, Ip: v, Weight: 0, }
				remoteService.queue = append(remoteService.queue, c)
				remoteService.ipList = append(remoteService.ipList, v)
			}
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
