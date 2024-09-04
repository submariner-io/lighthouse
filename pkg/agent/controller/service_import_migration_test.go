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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Legacy ServiceImport migration", func() {
	Describe("after restart on upgrade with a service that was previously exported", testLegacyServiceImportMigration)
})

var _ = Describe("Pre-clusterset IP ServiceImport migration", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace),
				Annotations: map[string]string{
					mcsv1a1.LabelServiceName:       serviceName,
					constants.LabelSourceNamespace: serviceNamespace,
				},
			},
			Spec: mcsv1a1.ServiceImportSpec{
				Type: mcsv1a1.ClusterSetIP,
			},
		})

		t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
		t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
			ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: ptr.To(int32(10))},
		}

		t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
		t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig

		t.cluster1.createServiceEndpointSlices()
		t.cluster1.createService()
		t.cluster1.createServiceExport()

		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	It("should update the existing aggregated ServiceImport and not create any Conflict conditions", func() {
		t.awaitNonHeadlessServiceExported(&t.cluster1)

		t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
	})
})

func testLegacyServiceImportMigration() {
	var (
		t                   *testDriver
		legacyServiceImport *mcsv1a1.ServiceImport
	)

	BeforeEach(func() {
		t = newTestDiver()

		t.cluster1.createServiceEndpointSlices()
		t.cluster1.createService()

		legacyServiceImport = t.newLegacyServiceImport(t.cluster1.clusterID)

		test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), legacyServiceImport)

		test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
			test.SetClusterIDLabel(legacyServiceImport, t.cluster1.clusterID))

		legacyEndpointSlice := &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s-%s", t.cluster1.service.Name, t.cluster1.service.Namespace, t.cluster1.clusterID),
				Labels: map[string]string{
					discovery.LabelManagedBy:        constants.LabelValueManagedBy,
					constants.MCSLabelSourceCluster: t.cluster1.clusterID,
					mcsv1a1.LabelServiceName:        t.cluster1.service.Name,
					constants.LabelSourceNamespace:  t.cluster1.service.Namespace,
				},
			},
			AddressType: discovery.AddressTypeIPv4,
			Endpoints:   t.cluster1.headlessEndpointAddresses[0],
			Ports: []discovery.EndpointPort{
				{
					Name:     &t.cluster1.service.Spec.Ports[0].Name,
					Protocol: &t.cluster1.service.Spec.Ports[0].Protocol,
					Port:     &t.cluster1.service.Spec.Ports[0].Port,
				},
				{
					Name:     &t.cluster1.service.Spec.Ports[1].Name,
					Protocol: &t.cluster1.service.Spec.Ports[1].Protocol,
					Port:     &t.cluster1.service.Spec.Ports[1].Port,
				},
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, legacyEndpointSlice)

		legacyEndpointSlice.Labels["submariner-io/originatingNamespace"] = t.cluster1.service.Namespace
		test.CreateResource(t.brokerEndpointSliceClient, test.SetClusterIDLabel(legacyEndpointSlice, t.cluster1.clusterID))

		t.cluster1.createServiceExport()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	It("should update the local legacy ServiceImport labels and re-export the service", func() {
		t.awaitNonHeadlessServiceExported(&t.cluster1)
		test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)
	})

	Context("", func() {
		BeforeEach(func() {
			Expect(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace).Delete(context.Background(),
				legacyServiceImport.Name, metav1.DeleteOptions{})).To(Succeed())
			t.cluster1.deleteServiceExport()
		})

		It("should not sync the local legacy ServiceImport from the broker", func() {
			Consistently(func() bool {
				_, err := t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace).Get(context.Background(),
					legacyServiceImport.Name, metav1.GetOptions{})
				return apierrors.IsNotFound(err)
			}, time.Millisecond*300).Should(BeTrue())
		})
	})

	Context("and the ServiceExport no longer exists", func() {
		BeforeEach(func() {
			t.cluster1.deleteServiceExport()
		})

		It("should delete the local legacy ServiceImport", func() {
			test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), legacyServiceImport.Name)
			t.awaitNoEndpointSlice(&t.cluster1)
			test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)
		})
	})

	When("there's existing legacy ServiceImports for other clusters", func() {
		var (
			remoteServiceImport1 *mcsv1a1.ServiceImport
			remoteServiceImport2 *mcsv1a1.ServiceImport
			remoteServiceImport3 *mcsv1a1.ServiceImport
		)

		BeforeEach(func() {
			remoteServiceImport1 = t.newLegacyServiceImport("region1")
			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), remoteServiceImport1)
			test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
				test.SetClusterIDLabel(remoteServiceImport1, remoteServiceImport1.Status.Clusters[0].Cluster))

			remoteServiceImport2 = t.newLegacyServiceImport("region2")
			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), remoteServiceImport2)
			test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
				test.SetClusterIDLabel(remoteServiceImport2, remoteServiceImport2.Status.Clusters[0].Cluster))

			remoteServiceImport3 = t.newLegacyServiceImport("region3")
			test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), remoteServiceImport3)
			test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
				test.SetClusterIDLabel(remoteServiceImport3, remoteServiceImport3.Status.Clusters[0].Cluster))
		})

		Context("that haven't been upgraded yet", func() {
			It("should retain the local legacy ServiceImport on the broker", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1)
				ensureServiceImport(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)
			})
		})

		Context("and after upgrading the clusters", func() {
			It("should eventually remove the local legacy ServiceImport from the broker", func() {
				t.awaitAggregatedServiceImport(mcsv1a1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)

				// Get the aggregated ServiceImport on the broker.

				aggregatedServiceImport := getServiceImport(t.brokerServiceImportClient, test.RemoteNamespace,
					fmt.Sprintf("%s-%s", t.cluster1.service.Name, t.cluster1.service.Namespace))

				By(fmt.Sprintf("Upgrade the first remote cluster %q", remoteServiceImport1.Status.Clusters[0].Cluster))

				aggregatedServiceImport.Status.Clusters = append(aggregatedServiceImport.Status.Clusters, mcsv1a1.ClusterStatus{
					Cluster: remoteServiceImport1.Status.Clusters[0].Cluster,
				})

				test.UpdateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImport)

				// Since the other remote clusters aren't upgraded yet, it shouldn't delete the local legacy ServiceImport
				// from the broker yet.

				ensureServiceImport(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)

				// Remove the legacy ServiceImport for remote cluster 3 from the broker.

				Expect(t.brokerServiceImportClient.Namespace(test.RemoteNamespace).Delete(context.Background(),
					remoteServiceImport3.Name, metav1.DeleteOptions{})).To(Succeed())
				test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), remoteServiceImport3.Name)

				By(fmt.Sprintf("Upgrade the second remote cluster %q", remoteServiceImport2.Status.Clusters[0].Cluster))

				aggregatedServiceImport.Status.Clusters = append(aggregatedServiceImport.Status.Clusters, mcsv1a1.ClusterStatus{
					Cluster: remoteServiceImport2.Status.Clusters[0].Cluster,
				})

				test.UpdateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImport)

				// Since we also deleted the legacy ServiceImport from remote cluster 3, it should observe that all remote
				// clusters have effectively been upgraded and thus should delete the local legacy ServiceImport from the broker.

				test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)

				// Ensure the sync from the broker doesn't delete the local ServiceImport.

				ensureServiceImport(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), legacyServiceImport.Name)

				Expect(t.brokerServiceImportClient.Namespace(test.RemoteNamespace).Delete(context.Background(),
					remoteServiceImport1.Name, metav1.DeleteOptions{})).To(Succeed())
				test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), remoteServiceImport1.Name)
			})
		})

		Context("that have already been upgraded", func() {
			BeforeEach(func() {
				test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &mcsv1a1.ServiceImport{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("%s-%s", t.cluster1.service.Name, t.cluster1.service.Namespace),
						Annotations: map[string]string{
							mcsv1a1.LabelServiceName:       t.cluster1.service.Name,
							constants.LabelSourceNamespace: t.cluster1.service.Namespace,
						},
					},
					Spec: mcsv1a1.ServiceImportSpec{
						Type:  mcsv1a1.ClusterSetIP,
						Ports: []mcsv1a1.ServicePort{port1, port2},
					},
					Status: mcsv1a1.ServiceImportStatus{
						Clusters: []mcsv1a1.ClusterStatus{
							{
								Cluster: remoteServiceImport1.Status.Clusters[0].Cluster,
							},
							{
								Cluster: remoteServiceImport2.Status.Clusters[0].Cluster,
							},
							{
								Cluster: remoteServiceImport3.Status.Clusters[0].Cluster,
							},
						},
					},
				})
			})

			It("should remove the local legacy ServiceImport from the broker", func() {
				test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), legacyServiceImport.Name)
			})
		})
	})
}

func ensureServiceImport(client dynamic.ResourceInterface, name string) {
	Consistently(func() bool {
		_, err := client.Get(context.Background(), name, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}, time.Millisecond*300).Should(BeFalse())
}

func (t *testDriver) newLegacyServiceImport(clusterID string) *mcsv1a1.ServiceImport {
	name := t.cluster1.service.Name
	namespace := t.cluster1.service.Namespace

	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s-%s", name, namespace, clusterID),
			Annotations: map[string]string{
				"origin-name":      name,
				"origin-namespace": namespace,
			},
			Labels: map[string]string{
				controller.LegacySourceNameLabel:    name,
				constants.LabelSourceNamespace:      namespace,
				controller.LegacySourceClusterLabel: clusterID,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  mcsv1a1.ClusterSetIP,
			IPs:   []string{"1.2.3.4"},
			Ports: []mcsv1a1.ServicePort{port1, port2},
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID,
				},
			},
		},
	}
}
