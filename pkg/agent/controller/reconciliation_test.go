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
	"strings"

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
	"k8s.io/client-go/testing"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Reconciliation", func() {
	var (
		t                    *testDriver
		serviceExport        *mcsv1a1.ServiceExport
		localServiceImport   *mcsv1a1.ServiceImport
		localEndpointSlice   *discovery.EndpointSlice
		brokerServiceImports *unstructured.UnstructuredList
		brokerEndpointSlices *unstructured.UnstructuredList
	)

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.cluster1.createServiceEndpointSlices()
		t.cluster1.createService()
		t.cluster1.createServiceExport()

		t.awaitNonHeadlessServiceExported(&t.cluster1)

		var err error

		brokerServiceImports, err = t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(context.TODO(), metav1.ListOptions{})
		Expect(err).To(Succeed())

		brokerEndpointSlices, err = t.brokerEndpointSliceClient.List(context.TODO(), metav1.ListOptions{})
		Expect(err).To(Succeed())

		localServiceImport = t.cluster1.findLocalServiceImport()
		Expect(localServiceImport).ToNot(BeNil())

		endpointSlices := t.cluster1.findLocalEndpointSlices()
		Expect(endpointSlices).To(HaveLen(1))
		localEndpointSlice = endpointSlices[0]

		obj, err := t.cluster1.localServiceExportClient.Get(context.Background(), t.cluster1.serviceExport.Name, metav1.GetOptions{})
		Expect(err).To(Succeed())
		serviceExport = toServiceExport(obj)
	})

	AfterEach(func() {
		t.afterEach()
	})

	restoreBrokerResources := func() {
		for i := range brokerServiceImports.Items {
			test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &brokerServiceImports.Items[i])
		}

		for i := range brokerEndpointSlices.Items {
			test.CreateResource(t.brokerEndpointSliceClient, &brokerEndpointSlices.Items[i])
		}
	}

	Context("on restart after a service was exported", func() {
		It("should retain the exported resources on reconciliation", func() {
			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, localEndpointSlice)
			test.CreateResource(t.cluster1.localServiceExportClient, serviceExport)

			restoreBrokerResources()

			t.cluster1.createServiceEndpointSlices()
			t.cluster1.createService()

			t.cluster1.start(t, *t.syncerConfig)
			t.cluster2.start(t, *t.syncerConfig)

			t.awaitNonHeadlessServiceExported(&t.cluster1)

			testutil.EnsureNoActionsForResource(&t.cluster1.localDynClient.Fake, "serviceimports", "delete")
			testutil.EnsureNoActionsForResource(&t.cluster1.localDynClient.Fake, "endpointslices", "delete")

			brokerDynClient := t.syncerConfig.BrokerClient.(*fake.FakeDynamicClient)
			testutil.EnsureNoActionsForResource(&brokerDynClient.Fake, "endpointslices", "delete")

			// For migration cleanup, it may attempt to delete a local legacy ServiceImport from the broker so ignore it.
			Consistently(func() bool {
				siActions := brokerDynClient.Fake.Actions()
				for i := range siActions {
					if siActions[i].GetResource().Resource == "serviceimports" && siActions[i].GetVerb() == "delete" &&
						!strings.Contains(siActions[i].(testing.DeleteAction).GetName(), t.cluster1.clusterID) {
						return true
					}
				}

				return false
			}).Should(BeFalse())
		})
	})

	When("a local ServiceImport is stale on startup due to a missed ServiceExport delete event", func() {
		It("should unexport the service on reconciliation", func() {
			t.afterEach()
			t = newTestDiver()

			restoreBrokerResources()
			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			test.CreateResource(t.cluster1.localEndpointSliceClient, localEndpointSlice)
			t.cluster1.createService()
			t.cluster1.start(t, *t.syncerConfig)

			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a local ServiceImport is stale on startup due to a missed Service delete event", func() {
		It("should unexport the service on reconciliation", func() {
			t.afterEach()
			t = newTestDiver()

			restoreBrokerResources()
			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport)
			t.cluster1.createServiceExport()
			t.cluster1.start(t, *t.syncerConfig)

			t.cluster1.awaitServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionFalse, "NoServiceImport"))
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a remote aggregated ServiceImport is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
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
			t.afterEach()
			t = newTestDiver()

			restoreBrokerResources()

			t.justBeforeEach()

			t.awaitNoAggregatedServiceImport(&t.cluster1)
		})
	})

	When("a local EndpointSlice is stale in the broker datastore on startup", func() {
		It("should delete it from the broker datastore on reconciliation", func() {
			endpointSlices := findEndpointSlices(t.brokerEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
			Expect(endpointSlices).To(HaveLen(1))
			endpointSlice := endpointSlices[0]

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.brokerEndpointSliceClient, endpointSlice)
			t.justBeforeEach()

			t.awaitNoEndpointSlice(&t.cluster1)
		})
	})

	When("a remote EndpointSlice is stale in the local datastore on startup", func() {
		It("should delete it from the local datastore on reconciliation", func() {
			endpointSlices := findEndpointSlices(t.cluster2.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
			Expect(endpointSlices).To(HaveLen(1))
			endpointSlice := endpointSlices[0]

			t.afterEach()
			t = newTestDiver()

			test.CreateResource(t.cluster2.localEndpointSliceClient, endpointSlice)
			t.cluster2.start(t, *t.syncerConfig)

			awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
		})
	})
})

var _ = Describe("EndpointSlice migration", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
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

	When("a legacy local EndpointSlice derived from Endpoints exists on startup", func() {
		epsName := fmt.Sprintf("nginx-%s-%s", serviceNamespace, clusterID1)

		JustBeforeEach(func() {
			eps := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      epsName,
					Namespace: serviceNamespace,
					Labels: map[string]string{
						discovery.LabelManagedBy:        constants.LabelValueManagedBy,
						constants.MCSLabelSourceCluster: clusterID1,
						mcsv1a1.LabelServiceName:        "nginx",
						constants.LabelIsHeadless:       strconv.FormatBool(true),
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
})
