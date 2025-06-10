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

package resolver

import (
	"sync"

	"github.com/submariner-io/lighthouse/coredns/loadbalancer"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/client-go/dynamic"
	k8snet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type Interface struct {
	serviceMap    map[string]*serviceInfo
	clusterStatus ClusterStatus
	client        dynamic.Interface
	mutex         sync.RWMutex
}

type ClusterStatus interface {
	IsConnected(clusterID string, ipFamily k8snet.IPFamily) bool
	GetLocalClusterID() string
}

type DNSRecord struct {
	IP          string
	Ports       []mcsv1a1.ServicePort
	HostName    string
	ClusterName string
}

type clusterInfo struct {
	endpointRecords       []DNSRecord
	endpointRecordsByHost map[string][]DNSRecord
	weight                int64
	endpointsHealthy      bool
}

type IPFamilyInfo struct {
	addrType discovery.AddressType
	clusters map[string]*clusterInfo
	balancer loadbalancer.Interface
	ports    []mcsv1a1.ServicePort
}

type serviceInfo struct {
	ipv4Info   IPFamilyInfo
	ipv6Info   IPFamilyInfo
	isExported bool
	spec       mcsv1a1.ServiceImportSpec
}
