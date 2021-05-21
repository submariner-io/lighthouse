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
	"github.com/coredns/coredns/plugin/pkg/dnsutil"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

// NOTE: This is taken from github.com/coredns/plugin/kubernetes/parse.go with changes to support use cases in
// https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api#dns
type recordRequest struct {
	// The hostname referring to individual pod backing a headless multiclusterservice.
	hostname string
	// The cluster referring to cluster exporting a multicluster service
	cluster string
	// The servicename used in Kubernetes.
	service string
	// The namespace used in Kubernetes.
	namespace string
	// A each name can be for a pod or a service, here we track what we've seen, either "pod" or "service".
	podOrSvc string
}

// parseRequest parses the qname to find all the elements we need for querying lighthouse.
// 3 Possible cases:
// 1. (host): host.cluster.service.namespace.pod|svc.zone
// 2. (cluster): cluster.service.namespace.pod|svc.zone
// 3. (service): service.namespace.pod|svc.zone
//
// Federations are handled in the federation plugin. And aren't parsed here.
func parseRequest(state request.Request) (r recordRequest, err error) {
	base, _ := dnsutil.TrimZone(state.Name(), state.Zone)
	// return NODATA for apex queries
	if base == "" || base == Svc || base == Pod {
		return r, nil
	}

	segs := dns.SplitDomainName(base)

	// for r.name, r.namespace and r.cluster, we need to know if they have been set or not...
	// For cluster: if empty we should skip the cluster check in k.get(). Hence we cannot set if to "*".
	// For name: myns.svc.cluster.local != *.myns.svc.cluster.local
	// For namespace: svc.cluster.local != *.svc.cluster.local

	// start at the right and fill out recordRequest with the bits we find, so we look for
	// pod|svc.namespace.service and then either
	// * cluster
	// * hostname.cluster

	last := len(segs) - 1
	if last < 0 {
		return r, nil
	}

	r.podOrSvc = segs[last]
	if r.podOrSvc != "pod" && r.podOrSvc != Svc {
		return r, errInvalidRequest
	}
	last--
	if last < 0 {
		return r, nil
	}

	r.namespace = segs[last]
	last--
	if last < 0 {
		return r, nil
	}

	r.service = segs[last]
	last--
	if last < 0 {
		return r, nil
	}

	// Because of ambiguity we check the labels left: 1: a cluster. 2: hostname and cluster.
	// Anything else is a query that is too long to answer and can safely be delegated to return an nxdomain.
	switch last {
	case 0: // cluster only
		r.cluster = segs[last]
	case 1: // cluster and hostname
		r.cluster = segs[last]
		r.hostname = segs[last-1]

	default: // too long
		return r, errInvalidRequest
	}

	return r, nil
}

// String return a string representation of r, it just returns all fields concatenated with dots.
// This is mostly used in tests.
func (r recordRequest) String() string {
	s := r.hostname
	s += "." + r.cluster
	s += "." + r.service
	s += "." + r.namespace
	s += "." + r.podOrSvc

	return s
}
