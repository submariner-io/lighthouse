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

package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	legacySourceNameLabel    = "lighthouse.submariner.io/sourceName"
	legacySourceClusterLabel = "lighthouse.submariner.io/sourceCluster"
)

var _ = Describe("Reconciliation", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a local headless ServiceImport is stale on startup due to a missed ServiceExport delete event", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete the ServiceImport and EndpointSlice on reconciliation", func() {
			serviceImport := t.cluster1.awaitServiceImport(t.service, mcsv1a1.Headless, "")
			endpointSlice := t.cluster1.awaitEndpointSlice(t)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, endpointSlice)
			t.createService()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
			t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed Service delete event", func() {
		It("should delete the ServiceImport on reconciliation", func() {
			serviceImport := t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)
			t.createServiceExport()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
			t.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "NoServiceImport"))
		})
	})

	When("a synced local ServiceImport is stale in the broker datastore on startup", func() {
		It("should delete it from the broker datastore on reconciliation", func() {
			serviceImport := t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerServiceImportClient, serviceImport)
			t.justBeforeEach()

			t.awaitServiceUnexported()
		})
	})

	When("a synced remote ServiceImport is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
			serviceImport := t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localServiceImportClient, serviceImport)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitServiceUnexported()
		})
	})

	When("a synced local EndpointSlice is stale in the broker datastore on startup", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete it from the broker datastore on reconciliation", func() {
			endpointSlice := t.awaitBrokerEndpointSlice()

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerEndpointSliceClient, endpointSlice)
			t.justBeforeEach()

			t.awaitNoEndpointSlice(t.brokerEndpointSliceClient)
			t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)
		})
	})

	When("a synced remote EndpointSlice is stale in the local datastore on startup", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		JustBeforeEach(func() {
			t.createEndpoints()
		})

		It("should delete it from the local datastore on reconciliation", func() {
			endpointSlice := t.cluster2.awaitEndpointSlice(t)

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localEndpointSliceClient, endpointSlice)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)
		})
	})

	When("a local EndpointSlice with the old naming convention sans namespace exists on startup", func() {
		epsName := "nginx-" + clusterID1

		JustBeforeEach(func() {
			eps := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      epsName,
					Namespace: serviceNamespace,
					Labels: map[string]string{
						discovery.LabelManagedBy:        constants.LabelValueManagedBy,
						constants.MCSLabelSourceCluster: clusterID1,
						mcsv1a1.LabelServiceName:        "nginx",
					},
				},
			}

			test.CreateResource(t.cluster1.localEndpointSliceClient, eps)

			eps.Namespace = test.RemoteNamespace
			test.CreateResource(t.brokerEndpointSliceClient, test.SetClusterIDLabel(eps, clusterID1))
		})

		It("should delete it", func() {
			test.AwaitNoResource(t.cluster1.localEndpointSliceClient, epsName)
			test.AwaitNoResource(t.brokerEndpointSliceClient, epsName)
		})
	})

	When("a local ServiceImport with the legacy source labels exists after restart", func() {
		var serviceImport *mcsv1a1.ServiceImport

		JustBeforeEach(func() {
			serviceImport = t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)

			t.afterEach()
			t = newTestDiver()

			serviceImport.Labels[legacySourceNameLabel] = serviceImport.Labels[mcsv1a1.LabelServiceName]
			delete(serviceImport.Labels, mcsv1a1.LabelServiceName)

			serviceImport.Labels[legacySourceClusterLabel] = serviceImport.Labels[constants.MCSLabelSourceCluster]
			delete(serviceImport.Labels, constants.MCSLabelSourceCluster)

			t.createService()
			t.createEndpoints()

			By("Restarting controller")
		})

		It("should update the ServiceImport labels and sync it", func() {
			t.cluster1.start(t, *t.syncerConfig)
			t.cluster2.start(t, *t.syncerConfig)

			By("Create the ServiceImport with the legacy labels")

			// We want to verify the transition behavior during the small window where the ServiceImport labels
			// haven't been migrated yet. This occurs when the ServiceExport is processed so to force the sequencing
			// create the ServiceImport with the legacy labels first then create the ServiceExport.

			test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)

			// The ServiceImport still has the legacy labels so shouldn't be synced to the broker yet.
			Eventually(func() *mcsv1a1.ServiceImport {
				return findServiceImport(t.brokerServiceImportClient, t.service.Namespace, t.service.Name, legacySourceNameLabel)
			}, 5*time.Second).Should(BeNil(), "Unexpected ServiceImport found")

			// The EndpointSlice shouldn't be created yet since the ServiceImport still has the legacy source cluster label.
			Consistently(func() *discovery.EndpointSlice {
				return findEndpointSlice(t.cluster1.localEndpointSliceClient, t.endpoints.Namespace, t.endpoints.Name)
			}).Should(BeNil(), "Unexpected EndpointSlice found")

			By("Create the ServiceExport")

			t.createServiceExport()

			serviceImport = t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, t.service.Spec.ClusterIP)
			Expect(serviceImport.Labels).ToNot(HaveKey(legacySourceNameLabel))
			Expect(serviceImport.Labels).ToNot(HaveKey(legacySourceClusterLabel))

			t.awaitServiceExported(t.service.Spec.ClusterIP)
			t.awaitEndpointSlice()
		})

		Context("and the ServiceExport no longer exists", func() {
			It("should delete the ServiceImport on reconciliation", func() {
				test.CreateResource(t.cluster1.localServiceImportClient, serviceImport)

				brokerSI := serviceImport.DeepCopy()
				brokerSI.Namespace = test.RemoteNamespace
				test.CreateResource(t.brokerServiceImportClient, brokerSI)

				t.cluster1.start(t, *t.syncerConfig)

				Eventually(func() *mcsv1a1.ServiceImport {
					return findServiceImport(t.cluster1.localServiceImportClient, t.service.Namespace, t.service.Name,
						legacySourceNameLabel)
				}, 5*time.Second).Should(BeNil(), "Unexpected ServiceImport found")

				Eventually(func() *mcsv1a1.ServiceImport {
					return findServiceImport(t.brokerServiceImportClient, t.service.Namespace, t.service.Name, legacySourceNameLabel)
				}, 5*time.Second).Should(BeNil(), "Unexpected ServiceImport found")
			})
		})
	})
})
