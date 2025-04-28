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

	k8snet "k8s.io/utils/net"
	"k8s.io/utils/set"
)

type ClusterStatus struct {
	mutex               sync.Mutex
	connectedClusterIDs map[k8snet.IPFamily]set.Set[string]
	localClusterID      atomic.Value
}

func NewClusterStatus(localClusterID string, isConnected ...string) *ClusterStatus {
	c := &ClusterStatus{
		connectedClusterIDs: map[k8snet.IPFamily]set.Set[string]{
			k8snet.IPv4: set.New(isConnected...),
			k8snet.IPv6: set.New[string](),
		},
	}

	c.localClusterID.Store(localClusterID)

	return c
}

func (c *ClusterStatus) IsConnected(clusterID string, ipFamily k8snet.IPFamily) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.connectedClusterIDs[ipFamily].Has(clusterID)
}

func (c *ClusterStatus) SetLocalClusterID(clusterID string) {
	c.localClusterID.Store(clusterID)
}

func (c *ClusterStatus) GetLocalClusterID() string {
	return c.localClusterID.Load().(string)
}

func (c *ClusterStatus) DisconnectAll(ipFamily k8snet.IPFamily) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs[ipFamily] = set.New[string]()
}

func (c *ClusterStatus) DisconnectClusterID(clusterID string, ipFamily k8snet.IPFamily) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs[ipFamily].Delete(clusterID)
}

func (c *ClusterStatus) ConnectClusterID(clusterID string, ipFamily k8snet.IPFamily) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.connectedClusterIDs[ipFamily].Insert(clusterID)
}
