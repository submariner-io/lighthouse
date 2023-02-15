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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Controller", func() {
	t := newTestDriver()

	expDNSRecord := resolver.DNSRecord{
		IP:          serviceIP1,
		Ports:       []mcsv1a1.ServicePort{port1},
		ClusterName: clusterID1,
	}

	When("a ServiceImport is created", func() {
		var serviceImport *mcsv1a1.ServiceImport

		BeforeEach(func() {
			serviceImport = newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1)
			t.createServiceImport(serviceImport)
		})

		Specify("GetDNSRecords should return the cluster's DNS record when requested", func() {
			t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
		})

		Context("and is subsequently deleted", func() {
			Specify("GetDNSRecords should eventually return no DNS record found", func() {
				t.awaitDNSRecords(namespace1, service1, clusterID1, "", true)

				err := t.serviceImports.Namespace(serviceImport.Namespace).Delete(context.TODO(), serviceImport.Name, metav1.DeleteOptions{})
				Expect(err).To(Succeed())

				t.awaitDNSRecords(namespace1, service1, clusterID1, "", false)
			})
		})
	})

	When("an EndpointSlice is created", func() {
		var endpointSlice *discovery.EndpointSlice

		JustBeforeEach(func() {
			endpointSlice = newClusterIPEndpointSlice(namespace1, service1, clusterID1, serviceIP1, true)
			t.createEndpointSlice(endpointSlice)
		})

		Context("before a ServiceImport", func() {
			Specify("GetDNSRecords should eventually deem the cluster healthy and return its DNS record", func() {
				Consistently(func() bool {
					_, _, found := t.resolver.GetDNSRecords(namespace1, service1, "", "")
					return found
				}).Should(BeFalse())

				t.createServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))

				t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)
			})
		})

		Context("after a ServiceImport", func() {
			BeforeEach(func() {
				t.createServiceImport(newClusterServiceImport(namespace1, service1, serviceIP1, clusterID1, port1))
			})

			Context("and then deleted", func() {
				Specify("GetDNSRecords should eventually deem the cluster unhealthy and return no DNS record", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)

					err := t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), endpointSlice.Name, metav1.DeleteOptions{})
					Expect(err).To(Succeed())

					t.awaitDNSRecordsFound(namespace1, service1, "", "", false)
				})
			})

			Context("and then updated to unhealthy", func() {
				Specify("GetDNSRecords should eventually return no DNS record", func() {
					t.awaitDNSRecordsFound(namespace1, service1, clusterID1, "", false, expDNSRecord)

					err := t.endpointSlices.Namespace(namespace1).Delete(context.TODO(), endpointSlice.Name, metav1.DeleteOptions{})
					Expect(err).To(Succeed())

					t.awaitDNSRecordsFound(namespace1, service1, "", "", false)
				})
			})
		})
	})
})
