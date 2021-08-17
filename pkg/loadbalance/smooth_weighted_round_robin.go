package loadbalance

import (
	"errors"
)

type weightedItem struct {
	Item            interface{}
	Weight          float64
	CurrentWeight   float64
	EffectiveWeight float64
}

// SmoothWeightedRR - Load Balancer implementation, Smooth Weighted Round Robin
type SmoothWeightedRR struct {
	items    []*weightedItem
	itemsMap map[interface{}]*weightedItem
}

// NewSWRR - SmoothWeightedRR - Load Balancer constructor
func NewSWRR() *SmoothWeightedRR {
	return &SmoothWeightedRR{
		items:    make([]*weightedItem, 0),
		itemsMap: make(map[interface{}]*weightedItem),
	}
}

func (lb *SmoothWeightedRR) itemsCount() int {
	return len(lb.items)
}

// Update - updates the weight of an existing item
func (lb *SmoothWeightedRR) Update(item interface{}, weight float64) (err error) {
	if item == nil {
		return errors.New("cannot Update nil interface")
	}

	if weight < 0 {
		return errors.New("does not support negative weigt")
	}

	var itemToUpdate = lb.itemsMap[item]
	if itemToUpdate == nil {
		return errors.New("no item to update")
	}

	itemToUpdate.Weight = weight

	return nil
}

// Add - adds a new unique item to the list
func (lb *SmoothWeightedRR) Add(item interface{}, weight float64) (err error) {
	if item == nil {
		return errors.New("cannot Add nil interface")
	}

	if weight < 0 {
		return errors.New("does not support negative weigt")
	}

	if lb.itemsMap[item] != nil {
		return errors.New("item already in queue")
	}

	weightedItem := &weightedItem{Item: item, Weight: weight, EffectiveWeight: weight}

	lb.itemsMap[item] = weightedItem
	lb.items = append(lb.items, weightedItem)

	return nil
}

// RemoveAll - removes all items and reset state
func (lb *SmoothWeightedRR) RemoveAll() {
	lb.items = lb.items[:0]
	lb.itemsMap = make(map[interface{}]*weightedItem)
}

// Reset - reset load balancing state, like adding all items with their original weights
func (lb *SmoothWeightedRR) Reset() {
	for _, s := range lb.items {
		s.EffectiveWeight = s.Weight
		s.CurrentWeight = 0
	}
}

// All - returns a map all items as keys and their weights as values
func (lb *SmoothWeightedRR) All() map[interface{}]float64 {
	m := make(map[interface{}]float64)
	for _, i := range lb.items {
		m[i.Item] = i.Weight
	}

	return m
}

// Next - fetches next item according to the smooth weighted round robin fashion
func (lb *SmoothWeightedRR) Next() interface{} {
	i := lb.nextWeightedItem()
	if i == nil {
		return nil
	}

	return i.Item
}

func (lb *SmoothWeightedRR) nextWeightedItem() *weightedItem {
	if lb.itemsCount() == 0 {
		return nil
	}

	if lb.itemsCount() == 1 {
		return lb.items[0]
	}

	return nextSmoothWeightedItem(lb.items)
}

func nextSmoothWeightedItem(items []*weightedItem) (best *weightedItem) {
	total := 0.0

	for i := 0; i < len(items); i++ {
		item := items[i]

		if item == nil {
			continue
		}

		item.CurrentWeight += item.EffectiveWeight
		total += item.EffectiveWeight
		if item.EffectiveWeight < item.Weight {
			item.EffectiveWeight++
		}

		if best == nil || item.CurrentWeight > best.CurrentWeight {
			best = item
		}
	}

	if best == nil {
		return nil
	}

	best.CurrentWeight -= total

	return best
}
