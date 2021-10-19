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
package lighthouse

import (
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog"
)

const (
	srcClusterKey      = "source_cluster"
	dstClusterKey      = "destination_cluster"
	dstSvcNameKey      = "destination_service_name"
	dstSvcIPKey        = "destination_service_ip"
	dstSvcNamespaceKey = "destination_service_namespace"

	ServiceDiscoveryQueryCounterName = "submariner_service_discovery_query_counter"
)

var dnsQueryCounter *prometheus.GaugeVec

func init() {
	klog.Infof("Initializing dns query counter")

	dnsQueryCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: ServiceDiscoveryQueryCounterName,
			Help: "Count the numebr of dns queries",
		},
		[]string{srcClusterKey, dstClusterKey, dstSvcNameKey, dstSvcNamespaceKey, dstSvcIPKey},
	)

	if err := prometheus.Register(dnsQueryCounter); err != nil {
		klog.Errorf("Failed to register: %s with prometheus due to: %v", ServiceDiscoveryQueryCounterName, err)
	}
}

func incDNSQueryCounter(srcCluster, dstCluster, dstSvcName, dstSvcNamespace, dstSvcIP string) {
	labels := prometheus.Labels{
		srcClusterKey:      srcCluster,
		dstClusterKey:      dstCluster,
		dstSvcNameKey:      dstSvcName,
		dstSvcNamespaceKey: dstSvcNamespace,
		dstSvcIPKey:        dstSvcIP,
	}

	dnsQueryCounter.With(labels).Inc()
}
