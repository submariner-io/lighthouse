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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
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
		t.cluster1.createEndpoints()
		t.cluster1.createService()
		t.cluster1.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	Context("on restart after a service was exported", func() {
		It("should retain the exported resources on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			localServiceImport := t.cluster1.findLocalServiceImport()
			Expect(localServiceImport).ToNot(BeNil())

			localEndpointSlice := t.cluster1.findLocalEndpointSlice()
			Expect(localEndpointSlice).ToNot(BeNil())

			brokerServiceImports, err := t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(Succeed())

			brokerEndpointSlices, err := t.brokerEndpointSliceClient.List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(Succeed())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, localEndpointSlice)

			for i := range brokerServiceImports.Items {
				test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &brokerServiceImports.Items[i])
			}

			for i := range brokerEndpointSlices.Items {
				test.CreateResource(t.brokerEndpointSliceClient, &brokerEndpointSlices.Items[i])
			}

			t.cluster1.createEndpoints()
			t.cluster1.createService()
			t.cluster1.createServiceExport()

			t.cluster1.start(t, *t.syncerConfig)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitNonHeadlessServiceExported(&t.cluster1)

			testutil.EnsureNoActionsForResource(&t.cluster1.localDynClient.Fake, "serviceimports", "delete")
			testutil.EnsureNoActionsForResource(&t.cluster1.localDynClient.Fake, "endpointslices", "delete")

			brokerDynClient := t.syncerConfig.BrokerClient.(*fake.FakeDynamicClient)
			testutil.EnsureNoActionsForResource(&brokerDynClient.Fake, "serviceimports", "delete")
			testutil.EnsureNoActionsForResource(&brokerDynClient.Fake, "endpointslices", "delete")
		})
	})

	When("a local ServiceImport is stale on startup due to a missed ServiceExport delete event", func() {
		It("should unexport the service on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			serviceImport := t.cluster1.findLocalServiceImport()
			Expect(serviceImport).ToNot(BeNil())

			endpointSlice := t.cluster1.findLocalEndpointSlice()
			Expect(endpointSlice).ToNot(BeNil())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), serviceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, endpointSlice)
			t.cluster1.createService()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed Service delete event", func() {
		It("should unexport the service on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			serviceImport := t.cluster1.findLocalServiceImport()
			Expect(serviceImport).ToNot(BeNil())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), serviceImport)
			t.cluster1.createServiceExport()
			t.cluster1.start(t, *t.syncerConfig)

			t.cluster1.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "NoServiceImport"))
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a remote aggregated ServiceImport is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			obj, err := t.cluster2.localServiceImportClient.Namespace(t.cluster1.service.Namespace).Get(context.TODO(),
				t.cluster1.service.Name, metav1.GetOptions{})
			Expect(err).To(Succeed())

			serviceImport := &mcsv1a1.ServiceImport{}
			Expect(scheme.Scheme.Convert(obj, serviceImport, nil)).To(Succeed())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localServiceImportClient.Namespace(t.cluster1.service.Namespace), serviceImport)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitNoAggregatedServiceImport(&t.cluster1)
		})
	})

	When("a remote aggregated ServiceImport in the broker datastore contains a stale cluster name on startup", func() {
		It("should delete it on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			brokerServiceImports, err := t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(Succeed())

			t.afterEach()
			t = newTestDiver()

			for i := range brokerServiceImports.Items {
				test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &brokerServiceImports.Items[i])
			}

			t.justBeforeEach()

			t.awaitNoAggregatedServiceImport(&t.cluster1)
		})
	})

	When("a local EndpointSlice is stale in the broker datastore on startup", func() {
		It("should delete it from the broker datastore on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			endpointSlice := findEndpointSlice(t.brokerEndpointSliceClient, t.cluster1.endpoints.Namespace,
				t.cluster1.endpoints.Name, t.cluster1.clusterID)
			Expect(endpointSlice).ToNot(BeNil())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerEndpointSliceClient, endpointSlice)
			t.justBeforeEach()

			t.awaitNoEndpointSlice(&t.cluster1)
		})
	})

	When("a remote EndpointSlice is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			endpointSlice := findEndpointSlice(t.cluster2.localEndpointSliceClient, t.cluster1.endpoints.Namespace,
				t.cluster1.endpoints.Name, t.cluster1.clusterID)
			Expect(endpointSlice).ToNot(BeNil())

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localEndpointSliceClient, endpointSlice)
			t.cluster2.start(t, *t.syncerConfig)

			awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
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
		var (
			serviceImport        *mcsv1a1.ServiceImport
			brokerServiceImports *unstructured.UnstructuredList
		)

		JustBeforeEach(func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			serviceImport = t.cluster1.findLocalServiceImport()
			Expect(serviceImport).ToNot(BeNil())

			var err error
			brokerServiceImports, err = t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(context.TODO(), metav1.ListOptions{})

			Expect(err).To(Succeed())

			t.afterEach()
			t = newTestDiver()

			serviceImport.Labels[legacySourceNameLabel] = serviceImport.Labels[mcsv1a1.LabelServiceName]
			delete(serviceImport.Labels, mcsv1a1.LabelServiceName)

			serviceImport.Labels[legacySourceClusterLabel] = serviceImport.Labels[constants.MCSLabelSourceCluster]
			delete(serviceImport.Labels, constants.MCSLabelSourceCluster)

			t.cluster1.createService()
			t.cluster1.createEndpoints()

			By("Restarting controller")
		})

		It("should update the ServiceImport labels and sync it", func() {
			t.cluster1.start(t, *t.syncerConfig)
			t.cluster2.start(t, *t.syncerConfig)

			By("Create the ServiceImport with the legacy labels")

			// We want to verify the transition behavior during the small window where the ServiceImport labels
			// haven't been migrated yet. This occurs when the ServiceExport is processed so to force the sequencing
			// create the ServiceImport with the legacy labels first then create the ServiceExport.

			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), serviceImport)

			// The ServiceImport still has the legacy labels so shouldn't be synced to the broker yet.
			Eventually(func() int {
				list, _ := t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(context.TODO(), metav1.ListOptions{})
				return len(list.Items)
			}, 5*time.Second).Should(BeZero(), "Unexpected ServiceImport found")

			// The EndpointSlice shouldn't be created yet since the ServiceImport still has the legacy source cluster label.
			Consistently(func() *discovery.EndpointSlice {
				return t.cluster1.findLocalEndpointSlice()
			}).Should(BeNil(), "Unexpected EndpointSlice found")

			By("Create the ServiceExport")

			t.cluster1.createServiceExport()

			t.awaitNoEndpointSlice(&t.cluster1)
		})

		Context("and the ServiceExport no longer exists", func() {
			It("should delete the ServiceImport on reconciliation", func() {
				test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), serviceImport)

				for i := range brokerServiceImports.Items {
					test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &brokerServiceImports.Items[i])
				}

				t.cluster1.start(t, *t.syncerConfig)
				t.cluster2.start(t, *t.syncerConfig)

				t.awaitNoAggregatedServiceImport(&t.cluster1)
			})
		})
	})
})
