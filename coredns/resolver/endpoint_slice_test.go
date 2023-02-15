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

package resolver_test

import (
	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/lighthouse/coredns/constants"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("PutEndpointSlice", func() {
	t := newTestDriver()

	When("the EndpointSlice is missing the required labels", func() {
		It("should not process it", func() {
			// Missing LabelServiceName
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace:  "test",
						constants.MCSLabelSourceCluster: "test",
					},
				},
			})

			// Missing LabelSourceNamespace
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.MCSLabelSourceCluster: "test",
						mcsv1a1.LabelServiceName:        "test",
					},
				},
			})

			// Missing MCSLabelSourceCluster
			t.putEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace: "test",
						mcsv1a1.LabelServiceName:       "test",
					},
				},
			})
		})
	})
})

var _ = Describe("RemoveEndpointSlice", func() {
	t := newTestDriver()

	When("the EndpointSlice is missing a required label", func() {
		It("should not process it", func() {
			t.resolver.RemoveEndpointSlice(&discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						constants.LabelSourceNamespace:  "test",
						constants.MCSLabelSourceCluster: "test",
					},
				},
			})
		})
	})

	When("the service information doesn't exist", func() {
		It("should not process it", func() {
			t.resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, nil))
		})
	})

	When("the cluster information doesn't exist", func() {
		It("should not process it", func() {
			t.resolver.PutServiceImport(newClusterHeadlessServiceImport(namespace1, service1, clusterID1))

			t.resolver.RemoveEndpointSlice(newEndpointSlice(namespace1, service1, clusterID1, nil))
		})
	})
})
