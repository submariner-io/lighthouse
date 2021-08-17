package loadbalance

// LoadBalance - general interface explaining the API of all load balancers available in the package
type LoadBalancer interface {
	// Next gets next selected item.
	Next() (item interface{})
	// Updates the weight of an item that is already in queue
	Update(item interface{}, weight float64) (err error)
	// Add adds a weighted item for selection. if not already in queue
	Add(item interface{}, weight float64) (err error)
	// All returns all items.
	All() map[interface{}]float64
	// RemoveAll removes all weighted items.
	RemoveAll()
	// Reset resets the balancing algorithm.
	Reset()
}
