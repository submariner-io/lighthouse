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

// Interface - general interface explaining the API of all load balancers available in the package.
type Interface interface {
	// Next returns next item accordingly or nil if none present.
	Next() (item interface{})
	// Skip selecting the given item for a full round. This is useful if the item encountered a temporary failure.
	Skip(item interface{})
	// Add adds a weighted item for selection, if not already present.
	Add(item interface{}, weight int64) (err error)
	// RemoveAll removes all weighted items.
	RemoveAll()
	// The number of items in this instance.
	ItemCount() int
}
