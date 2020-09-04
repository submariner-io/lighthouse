package endpointslice

import discovery "k8s.io/api/discovery/v1beta1"

type Store interface {
	Put(endpointSlice *discovery.EndpointSlice)

	Remove(endpointSlice *discovery.EndpointSlice)
}
