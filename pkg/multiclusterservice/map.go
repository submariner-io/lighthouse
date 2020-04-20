package multiclusterservice

import (
	"sync"

	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
)

type RemoteService struct {
	key         string
	ClusterInfo map[string]string
	IpList      []string
}

type Map struct {
	svcMap map[string]*RemoteService
	sync.RWMutex
}

func (m *Map) Get(namespace string, name string) (*RemoteService, bool) {
	return m.GetByKey(keyFunc(namespace, name))
}

func (m *Map) GetByKey(key string) (*RemoteService, bool) {
	m.RLock()
	defer m.RUnlock()
	value, ok := m.svcMap[key]
	return value, ok
}

func NewMap() *Map {
	return &Map{
		svcMap: make(map[string]*RemoteService),
	}
}

func (m *Map) Put(mcs *lighthousev1.MultiClusterService) {
	if name, ok := mcs.Annotations["origin-name"]; ok {
		namespace := mcs.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)
		m.Lock()
		defer m.Unlock()
		remoteService, ok := m.svcMap[key]
		if !ok {
			remoteService = &RemoteService{
				key:         key,
				ClusterInfo: make(map[string]string),
			}
		}
		for _, info := range mcs.Spec.Items {
			remoteService.ClusterInfo[info.ClusterID] = info.ServiceIP
		}
		remoteService.IpList = make([]string, 0)
		for _, v := range remoteService.ClusterInfo {
			remoteService.IpList = append(remoteService.IpList, v)
		}
		m.svcMap[key] = remoteService
	}
}

func (m *Map) Remove(mcs *lighthousev1.MultiClusterService) {
	if name, ok := mcs.Annotations["origin-name"]; ok {
		namespace := mcs.Annotations["origin-namespace"]
		key := keyFunc(namespace, name)
		m.Lock()
		defer m.Unlock()
		remoteService, ok := m.svcMap[key]
		if !ok {
			return
		}
		for _, info := range mcs.Spec.Items {
			delete(remoteService.ClusterInfo, info.ClusterID)
		}
		if len(remoteService.ClusterInfo) == 0 {
			delete(m.svcMap, key)
		} else {
			remoteService.IpList = make([]string, 0)
			for _, v := range remoteService.ClusterInfo {
				remoteService.IpList = append(remoteService.IpList, v)
			}
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
