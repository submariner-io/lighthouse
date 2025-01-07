/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package loadbalancer

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("LoadBalancer")}

type weightedItem struct {
	item            interface{}
	weight          int64
	currentWeight   int64
	effectiveWeight int64
}

// Smooth Weighted Round Robin load balancer implementation.
type smoothWeightedRR struct {
	items   []*weightedItem
	itemMap map[interface{}]*weightedItem
}

// NewSmoothWeightedRR returns a Smooth Weighted Round Robin load balancer.
func NewSmoothWeightedRR() Interface {
	return &smoothWeightedRR{
		items:   make([]*weightedItem, 0),
		itemMap: make(map[interface{}]*weightedItem),
	}
}

func (lb *smoothWeightedRR) Skip(item interface{}) {
	if wt, ok := lb.itemMap[item]; ok {
		wt.effectiveWeight -= wt.weight
		if wt.effectiveWeight < 0 {
			wt.effectiveWeight = 0
		}
	} else {
		logger.Errorf(nil, "Could not find item to skip: %v", item)
	}
}

// Number of Items added.
func (lb *smoothWeightedRR) ItemCount() int {
	return len(lb.items)
}

// Add - adds a new unique item to the list.
func (lb *smoothWeightedRR) Add(item interface{}, weight int64) error {
	if item == nil {
		return fmt.Errorf("item cannot be nil")
	}

	if weight < 0 {
		return fmt.Errorf("item weight %v cannot be negative", weight)
	}

	if lb.itemMap[item] != nil {
		return fmt.Errorf("item %v already present", item)
	}

	weightedItem := &weightedItem{item: item, weight: weight, currentWeight: 0, effectiveWeight: weight}

	lb.itemMap[item] = weightedItem
	lb.items = append(lb.items, weightedItem)

	return nil
}

// RemoveAll - removes all items and reset state.
func (lb *smoothWeightedRR) RemoveAll() {
	lb.items = lb.items[:0]
	lb.itemMap = make(map[interface{}]*weightedItem)
}

// Next - fetches the next item according to the smooth weighted round robin algorithm.
func (lb *smoothWeightedRR) Next() interface{} {
	i := lb.nextWeightedItem()
	if i == nil {
		return nil
	}

	return i.item
}

func (lb *smoothWeightedRR) nextWeightedItem() *weightedItem {
	itemsCount := len(lb.items)
	if itemsCount == 0 {
		return nil
	}

	if itemsCount == 1 {
		return lb.items[0]
	}

	return nextSmoothWeightedItem(lb.items)
}

func nextSmoothWeightedItem(items []*weightedItem) *weightedItem {
	var best *weightedItem
	total := int64(0)

	for _, item := range items {
		item.currentWeight += item.effectiveWeight

		if item.effectiveWeight < item.weight {
			item.effectiveWeight++
		}

		total += item.effectiveWeight

		if best == nil || item.currentWeight > best.currentWeight {
			best = item
		}
	}

	if best == nil {
		return nil
	}

	best.currentWeight -= total

	return best
}
