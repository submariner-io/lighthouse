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

package fake

import (
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/util/sets"
)

type ClusterStatus struct {
	mutex               sync.Mutex
	connectedClusterIDs sets.Set[string]
	localClusterID      atomic.Value
}

func NewClusterStatus(localClusterID string, isConnected ...string) *ClusterStatus {
	c := &ClusterStatus{
		connectedClusterIDs: sets.New(isConnected...),
	}

	c.localClusterID.Store(localClusterID)

	return c
}

func (c *ClusterStatus) IsConnected(clusterID string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.connectedClusterIDs.Has(clusterID)
}

func (c *ClusterStatus) SetLocalClusterID(clusterID string) {
	c.localClusterID.Store(clusterID)
}

func (c *ClusterStatus) GetLocalClusterID() string {
	return c.localClusterID.Load().(string)
}

func (c *ClusterStatus) DisconnectAll() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs = sets.New[string]()
}

func (c *ClusterStatus) DisconnectClusterID(clusterID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs.Delete(clusterID)
}

func (c *ClusterStatus) ConnectClusterID(clusterID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs.Insert(clusterID)
}
