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
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic/fake"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

var _ = Describe("Reconciliation", func() {
	var (
		t                            *testDriver
		serviceExport                *mcsv1b1.ServiceExport
		localServiceImport           *mcsv1b1.ServiceImport
		localAggregatedServiceImport *unstructured.Unstructured
		localEndpointSlice           *discovery.EndpointSlice
		brokerServiceImports         *unstructured.UnstructuredList
		brokerEndpointSlices         *unstructured.UnstructuredList
	)

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)

		t.cluster1.createServiceEndpointSlices(ctx)
		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)

		if t.cluster1.service.Spec.ClusterIP == corev1.ClusterIPNone {
			t.awaitHeadlessServiceExported(ctx, &t.cluster1)
		} else {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		}

		var err error

		brokerServiceImports, err = t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Succeed())

		brokerEndpointSlices, err = t.brokerEndpointSliceClient.List(ctx, metav1.ListOptions{})
		Expect(err).To(Succeed())

		localServiceImport = t.cluster1.findLocalServiceImport(ctx)
		Expect(localServiceImport).ToNot(BeNil())

		localAggregatedServiceImport, err = t.cluster1.localServiceImportClient.Namespace(serviceNamespace).Get(ctx,
			serviceName, metav1.GetOptions{})
		Expect(err).To(Succeed())
		localAggregatedServiceImport.SetResourceVersion("")

		endpointSlices := t.cluster1.findLocalEndpointSlices(ctx)
		Expect(endpointSlices).To(HaveLen(1))
		localEndpointSlice = endpointSlices[0]

		obj, err := t.cluster1.localServiceExportClient().Get(ctx, t.cluster1.serviceExport.Name, metav1.GetOptions{})
		Expect(err).To(Succeed())

		serviceExport = toServiceExport(obj)
	})

	AfterEach(func() {
		t.afterEach()
	})

	restoreBrokerResources := func(ctx context.Context) {
		for i := range brokerServiceImports.Items {
			test.CreateResource(ctx, t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &brokerServiceImports.Items[i])
		}

		for i := range brokerEndpointSlices.Items {
			test.CreateResource(ctx, t.brokerEndpointSliceClient, &brokerEndpointSlices.Items[i])
		}
	}

	Context("on restart after a service was exported", func() {
		BeforeEach(func() {
			t.useClusterSetIP = true
			t.cluster1.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
		})

		It("should retain the exported resources on reconciliation", func(ctx context.Context) {
			t.afterEach()
			t = newTestDiver(ctx)
			t.useClusterSetIP = true

			// Re-initialize aggregatedIPFamilies from the saved localAggregatedServiceImport
			t.aggregatedIPFamilies = toServiceImport(localAggregatedServiceImport).Spec.IPFamilies

			brokerDynClient := t.syncerConfig.BrokerClient.(*fake.FakeDynamicClient)

			// Use the broker client for cluster1 to simulate the broker being on the same cluster.
			t.cluster1.init(ctx, t.syncerConfig, brokerDynClient, &brokerDynClient.Fake)

			test.CreateResource(ctx, t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, localEndpointSlice)
			test.CreateResource(ctx, t.cluster1.localServiceExportClient(), serviceExport)

			_, err := t.cluster1.localServiceImportClient.Namespace(serviceNamespace).Create(ctx, localAggregatedServiceImport,
				metav1.CreateOptions{})
			Expect(err).To(Succeed())

			restoreBrokerResources(ctx)

			t.cluster1.createService(ctx)

			t.cluster1.start(ctx, t, *t.syncerConfig)
			t.cluster2.start(ctx, t, *t.syncerConfig)

			t.cluster1.createServiceEndpointSlices(ctx)

			testutil.EnsureNoActionsForResource(&brokerDynClient.Fake, "endpointslices", "delete")
			testutil.EnsureNoActionsForResource(&brokerDynClient.Fake, mcsv1b1.ServiceImportPluralName, "delete")

			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed ServiceExport delete event", func() {
		It("should unexport the service on reconciliation", func(ctx context.Context) {
			t.afterEach()
			t = newTestDiver(ctx)

			restoreBrokerResources(ctx)

			test.CreateResource(ctx, t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, localEndpointSlice)

			t.cluster1.createService(ctx)

			t.cluster1.start(ctx, t, *t.syncerConfig)

			t.cluster1.createServiceEndpointSlices(ctx)

			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed Service delete event", func() {
		It("should unexport the service on reconciliation", func(ctx context.Context) {
			t.afterEach()
			t = newTestDiver(ctx)

			restoreBrokerResources(ctx)
			test.CreateResource(ctx, t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, localEndpointSlice)
			t.cluster1.createServiceExport(ctx)
			t.cluster1.start(ctx, t, *t.syncerConfig)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse,
				controller.ServiceExportReasonNoServiceImport))
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	When("a remote aggregated ServiceImport is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func(ctx context.Context) {
			serviceImport := getServiceImport(ctx, t.cluster2.localServiceImportClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name)

			t.afterEach()
			t = newTestDiver(ctx)

			test.CreateResource(ctx, t.cluster2.localServiceImportClient.Namespace(t.cluster1.service.Namespace), serviceImport)
			t.cluster2.start(ctx, t, *t.syncerConfig)

			t.awaitNoAggregatedServiceImport(ctx, &t.cluster1)
		})
	})

	When("a remote aggregated ServiceImport in the broker datastore contains a stale cluster name on startup", func() {
		It("should delete it on reconciliation", func(ctx context.Context) {
			t.afterEach()
			t = newTestDiver(ctx)

			restoreBrokerResources(ctx)

			t.justBeforeEach(ctx)

			t.awaitNoAggregatedServiceImport(ctx, &t.cluster1)
		})
	})

	When("a local EndpointSlice is stale in the broker datastore on startup", func() {
		It("should delete it from the broker datastore on reconciliation", func(ctx context.Context) {
			endpointSlices := findEndpointSlices(ctx, t.brokerEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
			Expect(endpointSlices).To(HaveLen(1))
			endpointSlice := endpointSlices[0]

			t.afterEach()
			t = newTestDiver(ctx)

			test.CreateResource(ctx, t.brokerEndpointSliceClient, endpointSlice)
			t.justBeforeEach(ctx)

			t.awaitNoEndpointSlice(ctx, &t.cluster1)
		})
	})

	When("a remote EndpointSlice is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func(ctx context.Context) {
			endpointSlices := findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
			Expect(endpointSlices).To(HaveLen(1))
			endpointSlice := endpointSlices[0]

			t.afterEach()
			t = newTestDiver(ctx)

			test.CreateResource(ctx, t.cluster2.localEndpointSliceClient, endpointSlice)
			t.cluster2.start(ctx, t, *t.syncerConfig)

			awaitNoEndpointSlice(ctx, t.cluster2.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
		})
	})

	When("a local EndpointSlice is stale on startup", func() {
		Context("because the service no longer exists", func() {
			It("should delete it from the local datastore", func(ctx context.Context) {
				t.afterEach()
				t = newTestDiver(ctx)

				By("Restarting controllers")

				restoreBrokerResources(ctx)
				test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, localEndpointSlice)
				t.cluster1.start(ctx, t, *t.syncerConfig)

				t.awaitServiceUnexported(ctx, &t.cluster1)
			})
		})

		Context("because the K8s EndpointSlice no longer exists", func() {
			BeforeEach(func() {
				t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone
			})

			It("should delete it from the local datastore", func(ctx context.Context) {
				t.afterEach()
				t = newTestDiver(ctx)

				t.cluster1.service.Spec.ClusterIP = corev1.ClusterIPNone

				By("Restarting controllers")

				restoreBrokerResources(ctx)
				test.CreateResource(ctx, t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
				test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, localEndpointSlice)
				test.CreateResource(ctx, t.cluster1.localServiceExportClient(), serviceExport)
				t.cluster1.createService(ctx)

				// Create a remote EPS for the same service and ensure it's not deleted.
				remoteEndpointSlice := localEndpointSlice.DeepCopy()
				remoteEndpointSlice.Name = "remote-eps"
				remoteEndpointSlice.Labels[mcsv1b1.LabelSourceCluster] = t.cluster2.clusterID
				remoteEndpointSlice.Labels[federate.ClusterIDLabelKey] = t.cluster2.clusterID
				test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, remoteEndpointSlice)

				remoteEndpointSlice.Namespace = test.RemoteNamespace
				test.CreateResource(ctx, t.brokerEndpointSliceClient, remoteEndpointSlice)

				// Create an EPS for a service in another namespace and ensure it's not deleted.
				otherNS := "other-ns"
				otherNSEndpointSlice := localEndpointSlice.DeepCopy()
				otherNSEndpointSlice.Name = "other-ns-eps"
				otherNSEndpointSlice.Namespace = otherNS
				otherNSEndpointSlice.Labels[constants.LabelSourceNamespace] = otherNS
				test.CreateResource(ctx, endpointSliceClientFor(t.cluster1.localDynClient, otherNS), otherNSEndpointSlice)

				test.CreateResource(ctx, t.cluster1.dynamicServiceClientFor().Namespace(otherNS), &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      t.cluster1.service.Name,
						Namespace: otherNS,
					},
				})

				t.cluster1.start(ctx, t, *t.syncerConfig)

				t.awaitNoEndpointSlice(ctx, &t.cluster1)

				Consistently(func() bool {
					test.AwaitResource(ctx, t.cluster1.localEndpointSliceClient, remoteEndpointSlice.Name)
					return true
				}).Should(BeTrue())

				Consistently(func() bool {
					test.AwaitResource(ctx, endpointSliceClientFor(t.cluster1.localDynClient, otherNS), otherNSEndpointSlice.Name)
					return true
				}).Should(BeTrue())
			})
		})
	})
})

var _ = Describe("EndpointSlice migration", func() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a local EndpointSlice with the old naming convention sans namespace exists on startup", func() {
		epsName := "nginx-" + clusterID1

		JustBeforeEach(func(ctx context.Context) {
			eps := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      epsName,
					Namespace: serviceNamespace,
					Labels: map[string]string{
						discovery.LabelManagedBy:   constants.LabelValueManagedBy,
						mcsv1b1.LabelSourceCluster: clusterID1,
						mcsv1b1.LabelServiceName:   "nginx",
					},
				},
			}

			test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, eps)

			eps.Namespace = test.RemoteNamespace
			test.CreateResource(ctx, t.brokerEndpointSliceClient, test.SetClusterIDLabel(eps, clusterID1))
		})

		It("should delete it", func(ctx context.Context) {
			test.AwaitNoResource(ctx, t.cluster1.localEndpointSliceClient, epsName)
			test.AwaitNoResource(ctx, t.brokerEndpointSliceClient, epsName)
		})
	})

	When("a legacy local EndpointSlice derived from Endpoints exists on startup", func() {
		epsName := fmt.Sprintf("nginx-%s-%s", serviceNamespace, clusterID1)

		JustBeforeEach(func(ctx context.Context) {
			eps := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      epsName,
					Namespace: serviceNamespace,
					Labels: map[string]string{
						discovery.LabelManagedBy:   constants.LabelValueManagedBy,
						mcsv1b1.LabelSourceCluster: clusterID1,
						mcsv1b1.LabelServiceName:   "nginx",
						constants.LabelIsHeadless:  strconv.FormatBool(true),
					},
				},
			}

			test.CreateResource(ctx, t.cluster1.localEndpointSliceClient, eps)

			eps.Namespace = test.RemoteNamespace
			test.CreateResource(ctx, t.brokerEndpointSliceClient, test.SetClusterIDLabel(eps, clusterID1))
		})

		It("should delete it", func(ctx context.Context) {
			test.AwaitNoResource(ctx, t.cluster1.localEndpointSliceClient, epsName)
			test.AwaitNoResource(ctx, t.brokerEndpointSliceClient, epsName)
		})
	})
})
