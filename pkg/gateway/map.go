package gateway

import (
	"sync/atomic"
)

type Map struct {
	clustersMap atomic.Value
}

func NewMap() *Map {
	newMap := Map{}
	newMap.clustersMap.Store(make(map[string]bool))
	return &newMap
}

func (m *Map) Get() map[string]bool {
	clustersMap := m.clustersMap.Load()
	if clustersMap != nil {
		return clustersMap.(map[string]bool)
	}
	return make(map[string]bool)
}

func (m *Map) Store(gwMap map[string]bool) {
	m.clustersMap.Store(gwMap)
}
