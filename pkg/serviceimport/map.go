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

func (m *Map) selectIP(queue []clusterInfo, counter *uint64, checkCluster func(string) bool) string {
	queueLength := len(queue)
	for i := 0; i < queueLength; i++ {
		c := atomic.LoadUint64(counter)

		info := queue[c%uint64(queueLength)]

		atomic.AddUint64(counter, 1)

		if checkCluster(info.name) {
			return info.ip
		}
	}

	return ""
}

func (m *Map) GetIPs(namespace, name, cluster string, checkCluster func(string) bool) ([]string, bool) {
	clusterIPs, queue, counter, isHeadless := func() (map[string][]string, []clusterInfo, *uint64, bool) {
		m.RLock()
		defer m.RUnlock()

		si, ok := m.svcMap[keyFunc(namespace, name)]
		if !ok {
			return nil, nil, nil, false
		}

		return si.clusterIPs, si.clustersQueue, &si.rrCount, si.isHeadless
	}()

	if clusterIPs == nil {
		return nil, false
	}

	if cluster != "" {
		ips, found := clusterIPs[cluster]
		return ips, found
	}

	if !isHeadless {
		ip := m.selectIP(queue, counter, checkCluster)
		if ip != "" {
			return []string{ip}, true
		}

		return []string{}, true
	}

	serviceIPs := make([]string, 0)

	for cluster, ips := range clusterIPs {
		if checkCluster(cluster) {
			serviceIPs = append(serviceIPs, ips...)
		}
	}

	return serviceIPs, true
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

		if !remoteService.isHeadless {
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
