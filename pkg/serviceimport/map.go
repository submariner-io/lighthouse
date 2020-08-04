package serviceimport

import (
	"sync"
	"sync/atomic"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
)

type clusterInfo struct {
	ip     string
	name   string
	weight uint64
}

type serviceInfo struct {
	key           string
	clusterIPs    map[string][]string
	clustersQueue []clusterInfo
	rrCount       uint64
	isHeadless    bool
}

func (si *serviceInfo) buildClusterInfoQueue() {
	si.clustersQueue = make([]clusterInfo, 0)
	for k, v := range si.clusterIPs {
		if len(v) > 0 {
			c := clusterInfo{name: k, ip: v[0], weight: 0}
			si.clustersQueue = append(si.clustersQueue, c)
		}
	}
}

type Map struct {
	svcMap map[string]*serviceInfo
	sync.RWMutex
}

func (m *Map) selectIP(si *serviceInfo, checkCluster func(string) bool) (string, bool) {
	queue, counter := si.clustersQueue, &si.rrCount

	if queue == nil {
		return "", false
	}

	queueLength := len(queue)
	for i := 0; i < queueLength; i++ {
		c := atomic.LoadUint64(counter)

		info := queue[c%uint64(queueLength)]

		atomic.AddUint64(counter, 1)

		if checkCluster(info.name) {
			return info.ip, true
		}
	}

	return "", true
}

func (m *Map) GetIPs(namespace, name string, checkCluster func(string) bool) ([]string, bool) {
	var svcInfo *serviceInfo
	var ok bool

	serviceIps := make([]string, 0)

	m.RLock()
	defer m.RUnlock()

	if svcInfo, ok = m.svcMap[keyFunc(namespace, name)]; !ok {
		return nil, false
	}

	if !svcInfo.isHeadless {
		ip, found := m.selectIP(svcInfo, checkCluster)
		if ip != "" {
			serviceIps = append(serviceIps, ip)
		}

		return serviceIps, found
	}

	for cluster, ips := range svcInfo.clusterIPs {
		if checkCluster(cluster) && len(ips) > 0 {
			serviceIps = append(serviceIps, ips...)
		}
	}

	return serviceIps, true
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
				key:        key,
				clusterIPs: make(map[string][]string),
				rrCount:    0,
				isHeadless: serviceImport.Spec.Type == lighthousev2a1.Headless,
			}
		}

		for _, info := range serviceImport.Status.Clusters {
			remoteService.clusterIPs[info.Cluster] = info.IPs
		}

		if serviceImport.Spec.Type == lighthousev2a1.SuperclusterIP {
			remoteService.buildClusterInfoQueue()
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
			delete(remoteService.clusterIPs, info.Cluster)
		}

		if len(remoteService.clusterIPs) == 0 {
			delete(m.svcMap, key)
		} else if !remoteService.isHeadless {
			remoteService.buildClusterInfoQueue()
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
