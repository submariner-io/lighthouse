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
