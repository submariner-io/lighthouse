package multiclusterservice

import (
	"sync"

	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
)

type serviceInfo struct {
	key         string
	clusterInfo map[string]string
	ipList      []string
	rrCount     int // Counter for Round Ronbin IP selection, will be replaced by metirces object

}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

// NEW
func (m *Map) GetBestIP(namespace string, name string) (string, bool) {
	m.RLock()
	defer m.RUnlock()

	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		ipsCount := len(val.ipList)
		if ipsCount < 1 { return "", false  }
		selIP := val.ipList[val.rrCount%ipsCount]
		val.rrCount++
		return selIP ,true
	}
	return "", false
}

// OLD
func (m *Map) GetIps(namespace string, name string) ([]string, bool) {
	m.RLock()
	defer m.RUnlock()
	if val, ok := m.svcMap[keyFunc(namespace, name)]; ok {
		return val.ipList, len(val.ipList) > 0
	}
	return nil, false
}

func NewMap() *Map {
	return &Map{
		svcMap: make(map[string]*serviceInfo),
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
			remoteService = &serviceInfo{
				key:         key,
				clusterInfo: make(map[string]string),
			}
		}
		for _, info := range mcs.Spec.Items {
			remoteService.clusterInfo[info.ClusterID] = info.ServiceIP
		}
		remoteService.ipList = make([]string, 0)
		for _, v := range remoteService.clusterInfo {
			remoteService.ipList = append(remoteService.ipList, v)
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
			delete(remoteService.clusterInfo, info.ClusterID)
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
