package endpointslice

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/klog"
)

type endpointInfo struct {
	key         string
	clusterInfo map[string]*clusterInfo
}

type clusterInfo struct {
	hostIPs map[string][]string
	ipList  []string
}

type Map struct {
	epMap map[string]*endpointInfo
	sync.RWMutex
}

func (m *Map) GetIPs(hostname, cluster, namespace, name string, checkCluster func(string) bool) ([]string, bool) {
	key := keyFunc(name, namespace)

	clusterInfos := func() map[string]*clusterInfo {
		m.RLock()
		defer m.RUnlock()

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
		ips := make([]string, 0)

		for k, val := range clusterInfos {
			if checkCluster != nil && checkCluster(k) {
				ips = append(ips, val.ipList...)
			}
		}

		return ips, true
	case hostname == "":
		return clusterInfos[cluster].ipList, true
	default:
		return clusterInfos[cluster].hostIPs[hostname], true
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
		klog.V(log.DEBUG).Infof("Failed to get labels on %#v", es)
		return
	}

	cluster, ok := es.Labels[constants.LabelSourceCluster]

	if !ok {
		klog.V(log.DEBUG).Infof("Cluster label missing on %s", es.Name)
		return
	}

	epInfo, ok := m.epMap[key]
	if !ok {
		epInfo = &endpointInfo{
			key:         key,
			clusterInfo: make(map[string]*clusterInfo),
		}
	}

	m.Lock()
	defer m.Unlock()

	epInfo.clusterInfo[cluster] = &clusterInfo{
		ipList:  make([]string, 0),
		hostIPs: make(map[string][]string),
	}

	for _, endpoint := range es.Endpoints {
		if endpoint.Hostname != nil {
			epInfo.clusterInfo[cluster].hostIPs[*endpoint.Hostname] = endpoint.Addresses
		}

		epInfo.clusterInfo[cluster].ipList = append(epInfo.clusterInfo[cluster].ipList, endpoint.Addresses...)
	}

	klog.V(log.DEBUG).Infof("Adding endpointInfo %#v for %s in %s", epInfo.clusterInfo[cluster], es.Name, cluster)

	m.epMap[key] = epInfo
}

func (m *Map) Remove(es *discovery.EndpointSlice) {
	if key, ok := getKey(es); ok {
		cluster, ok := es.Labels[constants.LabelSourceCluster]

		if !ok {
			return
		}

		m.Lock()
		defer m.Unlock()

		epInfo, ok := m.epMap[key]
		if !ok {
			return
		}

		klog.V(log.DEBUG).Infof("Adding endpointInfo %#v for %s in %s", epInfo.clusterInfo[cluster], es.Name, cluster)
		delete(epInfo.clusterInfo, cluster)
	}
}

func getKey(es *discovery.EndpointSlice) (string, bool) {
	name, ok := es.Labels[constants.LabelSourceName]

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
