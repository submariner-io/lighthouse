package multiclusterservice

import (
	"sync"

	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
)

type Map struct {
	syncMap sync.Map
}

func (m *Map) Get(namespace, name string) (*lighthousev1.MultiClusterService, bool) {
	value, ok := m.syncMap.Load(keyFunc(namespace, name))
	if ok {
		return value.(*lighthousev1.MultiClusterService), true
	}

	return nil, false
}

func (m *Map) Put(mcs *lighthousev1.MultiClusterService) {
	m.syncMap.Store(keyFunc(mcs.Namespace, mcs.Name), mcs)
}

func (m *Map) Remove(namespace, name string) {
	m.syncMap.Delete(keyFunc(namespace, name))
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
