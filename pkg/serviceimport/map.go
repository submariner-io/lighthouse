package serviceimport

import (
	"sync"
	"sync/atomic"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type clusterInfo struct {
	ip     string
	name   string
	weight uint64
}

type serviceInfo struct {
	key           string
	clusterIPs    map[string]string
	clustersQueue []clusterInfo
	rrCount       uint64
	isHeadless    bool
}

func (si *serviceInfo) buildClusterInfoQueue() {
	si.clustersQueue = make([]clusterInfo, 0)
	for cluster, ip := range si.clusterIPs {
		c := clusterInfo{name: cluster, ip: ip, weight: 0}
		si.clustersQueue = append(si.clustersQueue, c)
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

func (m *Map) GetIP(namespace, name, cluster string, checkCluster func(string) bool) (string, bool) {
	clusterIPs, queue, counter, isHeadless := func() (map[string]string, []clusterInfo, *uint64, bool) {
		m.RLock()
		defer m.RUnlock()

		si, ok := m.svcMap[keyFunc(namespace, name)]
		if !ok {
			return nil, nil, nil, false
		}

		return si.clusterIPs, si.clustersQueue, &si.rrCount, si.isHeadless
	}()

	if clusterIPs == nil || isHeadless {
		return "", false
	}

	if cluster != "" {
		ip, found := clusterIPs[cluster]
		return ip, found
	}

	ip := m.selectIP(queue, counter, checkCluster)
	if ip != "" {
		return ip, true
	}

	return "", true
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
				clusterIPs: make(map[string]string),
				rrCount:    0,
				isHeadless: serviceImport.Spec.Type == mcsv1a1.Headless,
			}
		}

		remoteService.clusterIPs[serviceImport.GetLabels()[lhconstants.LabelSourceCluster]] = serviceImport.Spec.IPs[0]

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
