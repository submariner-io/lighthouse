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

type weightedItem[T comparable] struct {
	item            T
	weight          int64
	currentWeight   int64
	effectiveWeight int64
}

// Smooth Weighted Round Robin load balancer implementation.
type smoothWeightedRR[T comparable] struct {
	items   []*weightedItem[T]
	itemMap map[T]*weightedItem[T]
}

// NewSmoothWeightedRR returns a Smooth Weighted Round Robin load balancer.
func NewSmoothWeightedRR[T comparable]() Interface[T] {
	return &smoothWeightedRR[T]{
		items:   make([]*weightedItem[T], 0),
		itemMap: make(map[T]*weightedItem[T]),
	}
}

func (lb *smoothWeightedRR[T]) Skip(item T) {
	if wt, ok := lb.itemMap[item]; ok {
		wt.effectiveWeight -= wt.weight
		if wt.effectiveWeight < 0 {
			wt.effectiveWeight = 0
		}
	} else {
		logger.Errorf(nil, "Could not find item to skip: %v", item)
	}
}

// ItemCount returns the number of items.
func (lb *smoothWeightedRR[T]) ItemCount() int {
	return len(lb.items)
}

// Add - adds a new unique item to the list.
func (lb *smoothWeightedRR[T]) Add(item T, weight int64) error {
	if weight < 0 {
		return fmt.Errorf("item weight %v cannot be negative", weight)
	}

	if lb.itemMap[item] != nil {
		return fmt.Errorf("item %v already present", item)
	}

	weightedItem := &weightedItem[T]{item: item, weight: weight, currentWeight: 0, effectiveWeight: weight}

	lb.itemMap[item] = weightedItem
	lb.items = append(lb.items, weightedItem)

	return nil
}

// Reset - removes all items and resets state while retaining capacity for reuse.
func (lb *smoothWeightedRR[T]) Reset() {
	clear(lb.items)
	lb.items = lb.items[:0]
	clear(lb.itemMap)
}

// Next - fetches the next item according to the smooth weighted round robin algorithm.
func (lb *smoothWeightedRR[T]) Next() (T, bool) {
	i := lb.nextWeightedItem()
	if i == nil {
		return *new(T), false
	}

	return i.item, true
}

func (lb *smoothWeightedRR[T]) nextWeightedItem() *weightedItem[T] {
	itemsCount := len(lb.items)
	if itemsCount == 0 {
		return nil
	}

	if itemsCount == 1 {
		return lb.items[0]
	}

	var best *weightedItem[T]
	total := int64(0)

	for _, item := range lb.items {
		item.currentWeight += item.effectiveWeight

		if item.effectiveWeight < item.weight {
			item.effectiveWeight++
		}

		total += item.effectiveWeight

		if best == nil || item.currentWeight > best.currentWeight {
			best = item
		}
	}

	best.currentWeight -= total

	return best
}
