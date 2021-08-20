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

// Interface - general interface explaining the API of all load balancers available in the package
type Interface interface {
	// Next gets next selected item.
	Next() (item interface{})
	// If the item fetched suffer from some failure or is not well suted, you can notify the lb to accomodate and not fetch it for a full Next() round
	ItemFailed(item interface{})
	// Add adds a weighted item for selection. if not already in queue
	Add(item interface{}, weight float64) (err error)
	// RemoveAll removes all weighted items.
	RemoveAll()
	// The amount of items to balance between
	ItemsCount() int
}
