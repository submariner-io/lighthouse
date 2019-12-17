package dnscontroller

import (
	"sync"

	"k8s.io/klog"

	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
)

type MultiClusterServiceMap struct {
	syncMap sync.Map
}

func (m *MultiClusterServiceMap) get(namespace, name string) (*lighthousev1.MultiClusterService, bool) {
	key := keyFunc(namespace, name)
	value, ok := m.syncMap.Load(key)
	klog.Errorf("Key is : %q, Value %q Ok %q", key, value, ok)
	m.syncMap.Range(func(key interface{}, value interface{}) bool {
		klog.Errorf("Key: %q, Value %q", key, value)
		return true
	})
	if ok {
		return value.(*lighthousev1.MultiClusterService), true
	}

	return nil, false
}

func (m *MultiClusterServiceMap) put(mcs *lighthousev1.MultiClusterService) {
	m.syncMap.Range(func(key interface{}, value interface{}) bool {
		klog.Errorf("Key: %q, Value %q", key, value)
		return true
	})
	m.syncMap.Store(keyFunc(mcs.Namespace, mcs.Name), mcs)
}

func (m *MultiClusterServiceMap) remove(namespace, name string) {
	m.syncMap.Delete(keyFunc(namespace, name))
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}
