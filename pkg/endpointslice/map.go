package endpointslice

import (
	"sync"

	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1beta1"
)

type endpointInfo struct {
	key     string
	hostIPs map[string][]string
}

type Map struct {
	epMap map[string]*endpointInfo
	sync.RWMutex
}

func (m *Map) GetIPs(hostname, cluster, namespace, name string) ([]string, bool) {
	key := keyFunc(namespace, name, cluster)

	m.RLock()
	defer m.RUnlock()

	result, ok := m.epMap[key]
	if !ok {
		return nil, false
	}

	ips, ok := result.hostIPs[hostname]

	return ips, ok
}

func NewMap() *Map {
	return &Map{
		epMap: make(map[string]*endpointInfo),
	}
}

func (m *Map) Put(es *discovery.EndpointSlice) {
	key, ok := es.Labels[constants.LabelServiceImportName]
	if !ok {
		return
	}

	epInfo := &endpointInfo{
		key:     key,
		hostIPs: make(map[string][]string),
	}

	m.Lock()
	defer m.Unlock()

	for _, endpoint := range es.Endpoints {
		if endpoint.Hostname != nil {
			epInfo.hostIPs[*endpoint.Hostname] = endpoint.Addresses
		}
	}

	m.epMap[key] = epInfo
}

func (m *Map) Remove(es *discovery.EndpointSlice) {
	if key, ok := es.Labels[constants.LabelServiceImportName]; ok {
		m.Lock()
		defer m.Unlock()
		delete(m.epMap, key)
	}
}

func keyFunc(namespace, name, cluster string) string {
	return name + "-" + namespace + "-" + cluster
}
