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
	"slices"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

var _ = Describe("ClusterIP Service export", func() {
	Describe("in single cluster", testClusterIPServiceInOneCluster)
	Describe("in two clusters", testClusterIPServiceInTwoClusters)
	Describe("with multiple service EndpointSlices", testClusterIPServiceWithMultipleEPS)
})

//nolint:maintidx // This function composes test cases so ignore low maintainability index.
func testClusterIPServiceInOneCluster() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)

		t.cluster1.createServiceEndpointSlices(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		Context("and the Service already exists", func() {
			It("should export the service and update the ServiceExport status", func(ctx context.Context) {
				t.cluster1.createService(ctx)
				t.cluster1.createServiceExport(ctx)
				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				t.cluster1.awaitServiceExportCondition(ctx,
					newServiceExportReadyCondition(metav1.ConditionFalse, mcsv1b1.ServiceExportReasonPending),
					newServiceExportReadyCondition(metav1.ConditionTrue, mcsv1b1.ServiceExportReasonExported))
				t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)

				By(fmt.Sprintf("Ensure cluster %q does not try to update the status for a non-existent ServiceExport",
					t.cluster2.clusterID))

				t.cluster2.ensureNoServiceExportActions()
			})
		})

		Context("and the Service doesn't initially exist", func() {
			It("should eventually export the service", func(ctx context.Context) {
				t.cluster1.createServiceExport(ctx)
				t.cluster1.awaitNoServiceStatus(ctx)

				t.cluster1.createService(ctx)
				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
			})
		})
	})

	When("a ServiceExport is deleted after the service is exported", func() {
		It("should unexport the service", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster1.deleteServiceExport(ctx)
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	When("an exported Service is deleted and recreated while the ServiceExport still exists", func() {
		It("should unexport and re-export the service", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
			t.cluster1.localDynClientFake.ClearActions()

			By("Deleting the service")
			t.cluster1.deleteService(ctx)
			t.cluster1.awaitNoServiceStatus(ctx)
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse,
				controller.ServiceExportReasonNoServiceImport))
			t.awaitServiceUnexported(ctx, &t.cluster1)

			By("Re-creating the service")
			t.cluster1.createService(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the type of an exported Service is updated to an unsupported type", func() {
		It("should unexport the ServiceImport and update the ServiceExport status appropriately", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster1.service.Spec.Type = corev1.ServiceTypeNodePort
			t.cluster1.updateService(ctx)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
				mcsv1b1.ServiceExportReasonInvalidServiceType))
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse,
				controller.ServiceExportReasonNoServiceImport))
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	When("a ServiceExport is created for a Service whose type is unsupported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.Type = corev1.ServiceTypeNodePort
		})

		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
		})

		It("should update the ServiceExport status appropriately and not export the serviceImport", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
				mcsv1b1.ServiceExportReasonInvalidServiceType))
			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionReady)
		})

		Context("and is subsequently updated to a supported type", func() {
			It("should eventually export the service and update the ServiceExport status appropriately", func(ctx context.Context) {
				t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
					mcsv1b1.ServiceExportReasonInvalidServiceType))

				t.cluster1.service.Spec.Type = corev1.ServiceTypeClusterIP
				t.cluster1.updateService(ctx)

				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
			})
		})
	})

	When("a ServiceExport is created for a Service whose namespace is restricted", func() {
		BeforeEach(func() {
			t.cluster1.service.Namespace = metav1.NamespaceSystem
			t.cluster1.serviceExport.Namespace = metav1.NamespaceSystem
		})

		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
		})

		It("should not export the service", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse,
				controller.ServiceExportReasonRestrictedNamespace))
			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionReady)
		})
	})

	When("the backend service EndpointSlice has no ready addresses", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})

		Specify("the exported EndpointSlice's service IP address should indicate not ready", func(ctx context.Context) {
			for i := range t.cluster1.serviceEndpointSlices[0].Endpoints {
				t.cluster1.serviceEndpointSlices[0].Endpoints[i].Conditions = discovery.EndpointConditions{Ready: new(false)}
			}

			t.cluster1.expectedClusterIPEndpoints[0].Conditions = discovery.EndpointConditions{Ready: new(false)}

			t.cluster1.updateServiceEndpointSlices(ctx)
			t.ensureEndpointSlice(ctx, &t.cluster1)
		})
	})

	When("the ports for an exported service are updated", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})

		It("should re-export the service with the updated ports", func(ctx context.Context) {
			t.cluster1.service.Spec.Ports = append(t.cluster1.service.Spec.Ports, toServicePort(port3))
			t.aggregatedServicePorts = append(t.aggregatedServicePorts, port3)

			t.cluster1.updateService(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the labels for an exported service are updated", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})

		It("should update the existing EndpointSlice labels", func(ctx context.Context) {
			existingEPS := findEndpointSlices(ctx, t.cluster1.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)[0]

			By("Updating service labels")

			newLabelName := "new-label"
			newLabelValue := "new-value"

			t.cluster1.service.Labels[newLabelName] = newLabelValue
			t.cluster1.serviceEndpointSlices[0].Labels[newLabelName] = newLabelValue

			t.cluster1.updateServiceEndpointSlices(ctx)

			Eventually(ctx, func(ctx context.Context) map[string]string {
				eps, err := t.cluster1.localEndpointSliceClient.Get(ctx, existingEPS.Name, metav1.GetOptions{})
				Expect(err).To(Succeed())

				return eps.GetLabels()
			}).Should(HaveKeyWithValue(newLabelName, newLabelValue))

			newSlices := findEndpointSlices(ctx, t.cluster1.localEndpointSliceClient, t.cluster1.service.Namespace,
				t.cluster1.service.Name, t.cluster1.clusterID)
			Expect(newSlices).To(HaveLen(1))
			Expect(newSlices[0].Name).To(Equal(existingEPS.Name))
		})
	})

	When("the session affinity is configured for an exported service", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(10))},
			}

			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
			t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
		})

		It("should be propagated to the ServiceImport", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the traffic distribution and policy are configured for an exported service", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(10))},
			}
			t.cluster1.service.Spec.InternalTrafficPolicy = new(corev1.ServiceInternalTrafficPolicyLocal)
			t.cluster1.service.Spec.TrafficDistribution = new("PreferClose")

			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
			t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
			t.aggregatedInternalTrafficPolicy = t.cluster1.service.Spec.InternalTrafficPolicy
			t.aggregatedTrafficDistribution = t.cluster1.service.Spec.TrafficDistribution
		})

		It("should be propagated to the ServiceImport", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	Context("with clusterset IP enabled", func() {
		BeforeEach(func() {
			t.useClusterSetIP = true
		})

		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
		})

		Context("via ServiceExport annotation", func() {
			BeforeEach(func() {
				t.cluster1.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
			})

			It("should allocate an IP for the aggregated ServiceImport and release the IP when unexported", func(ctx context.Context) {
				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

				localSI := getServiceImport(ctx, t.cluster1.localServiceImportClient, t.cluster1.service.Namespace,
					t.cluster1.service.Name)
				Expect(localSI.Annotations).To(HaveKeyWithValue(constants.ClustersetIPAllocatedBy, t.cluster1.clusterID))

				By("Unexporting the service")

				t.cluster1.deleteServiceExport(ctx)

				Eventually(func() error {
					return t.ipPool.Reserve(localSI.Spec.IPs...)
				}).Should(Succeed(), "ServiceImport IP was not released")
			})

			Context("but with no IP pool specified", func() {
				BeforeEach(func() {
					t.useClusterSetIP = false
					t.ipPool = nil
				})

				It("should not set the IP on the aggregated ServiceImport", func(ctx context.Context) {
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				})
			})

			Context("with the IP pool initially exhausted", func() {
				var ips []string

				BeforeEach(func() {
					var err error

					ips, err = t.ipPool.Allocate(t.ipPool.Size())
					Expect(err).To(Succeed())
				})

				It("should eventually set the IP on the aggregated ServiceImport", func(ctx context.Context) {
					t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse,
						mcsv1b1.ServiceExportReasonFailed))

					_ = t.ipPool.Release(ips...)

					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				})
			})
		})

		Context("via the global setting", func() {
			BeforeEach(func() {
				t.cluster1.agentSpec.ClustersetIPEnabled = true
			})

			It("should set the IP on the aggregated ServiceImport", func(ctx context.Context) {
				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
			})

			Context("but disabled via ServiceExport annotation", func() {
				BeforeEach(func() {
					t.useClusterSetIP = false
					t.cluster1.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(false)}
				})

				It("should not set the IP on the aggregated ServiceImport", func(ctx context.Context) {
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
				})
			})
		})
	})

	When("two Services with the same name in different namespaces are exported", func() {
		It("should correctly export both services", func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      t.cluster1.service.Name,
					Namespace: "other-service-ns",
				},
				Spec: corev1.ServiceSpec{
					ClusterIPs: []string{"10.253.9.2"},
				},
			}

			serviceExport := &mcsv1b1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
			}

			serviceEPS := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:   service.Name + "-abcde",
					Labels: map[string]string{discovery.LabelServiceName: serviceName},
				},
				AddressType: discovery.AddressTypeIPv4,
			}

			expServiceImport := &mcsv1b1.ServiceImport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
				Spec: mcsv1b1.ServiceImportSpec{
					Type:  mcsv1b1.ClusterSetIP,
					Ports: []mcsv1b1.ServicePort{},
				},
				Status: mcsv1b1.ServiceImportStatus{
					Clusters: []mcsv1b1.ClusterStatus{
						{
							Cluster: t.cluster1.clusterID,
						},
					},
				},
			}

			expEndpointSlice := &discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
					Labels: map[string]string{
						discovery.LabelManagedBy:       constants.LabelValueManagedBy,
						mcsv1b1.LabelSourceCluster:     t.cluster1.clusterID,
						mcsv1b1.LabelServiceName:       service.Name,
						constants.LabelSourceNamespace: service.Namespace,
						constants.LabelIsHeadless:      strconv.FormatBool(false),
					},
				},
				AddressType: discovery.AddressTypeIPv4,
				Endpoints: []discovery.Endpoint{
					{
						Addresses:  []string{service.Spec.ClusterIPs[0]},
						Conditions: discovery.EndpointConditions{Ready: new(false)},
					},
				},
			}

			test.CreateResource(ctx, endpointSliceClientFor(t.cluster1.localDynClient, service.Namespace), serviceEPS)
			test.CreateResource(ctx, t.cluster1.dynamicServiceClientFor().Namespace(service.Namespace), service)
			test.CreateResource(ctx, serviceExportClientFor(t.cluster1.localDynClient, service.Namespace), serviceExport)

			awaitServiceImport(ctx, t.cluster2.localServiceImportClient, expServiceImport, t.ipPool)
			awaitEndpointSlice(ctx, endpointSliceClientFor(t.cluster2.localDynClient, service.Namespace), service.Name, expEndpointSlice)

			// Ensure the resources for the first Service weren't overwritten
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)

			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict, serviceExport)
		})
	})

	Specify("an EndpointSlice not managed by Lighthouse should not be synced to the broker", func(ctx context.Context) {
		test.CreateResource(ctx, endpointSliceClientFor(t.cluster1.localDynClient, t.cluster1.service.Namespace),
			&discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
				Name:   "other-eps",
				Labels: map[string]string{discovery.LabelManagedBy: "other"},
			}})

		testutil.EnsureNoResource(ctx, resource.ForDynamic(endpointSliceClientFor(t.syncerConfig.BrokerClient,
			test.RemoteNamespace)), "other-eps")
	})

	When("the namespace of an exported service does not initially exist on an importing cluster", func() {
		createNamespace := func(ctx context.Context, dynClient dynamic.Interface, name string) {
			test.CreateResource(ctx, dynClient.Resource(corev1.SchemeGroupVersion.WithResource("namespaces")), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
			})
		}

		BeforeEach(func(ctx context.Context) {
			fake.AddVerifyNamespaceReactor(t.cluster2.localDynClientFake, mcsv1b1.ServiceImportPluralName, "endpointslices")

			createNamespace(ctx, t.cluster2.localDynClient, test.LocalNamespace)
		})

		JustBeforeEach(func(ctx context.Context) {
			t.cluster1.createService(ctx)
			t.cluster1.createServiceExport(ctx)
		})

		It("should eventually import the service when the namespace is created", func(ctx context.Context) {
			expServiceImport := &mcsv1b1.ServiceImport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      t.cluster1.service.Name,
					Namespace: t.cluster1.service.Namespace,
				},
				Spec: mcsv1b1.ServiceImportSpec{
					Type:            mcsv1b1.ClusterSetIP,
					Ports:           t.aggregatedServicePorts,
					IPFamilies:      t.aggregatedIPFamilies,
					SessionAffinity: corev1.ServiceAffinityNone,
				},
				Status: mcsv1b1.ServiceImportStatus{
					Clusters: []mcsv1b1.ClusterStatus{{Cluster: t.cluster1.clusterID}},
				},
			}

			awaitServiceImport(ctx, t.cluster1.localServiceImportClient, expServiceImport, t.ipPool)

			testutil.EnsureNoResource(ctx, resource.ForDynamic(t.cluster2.localServiceImportClient.Namespace(
				t.cluster1.service.Namespace)), t.cluster1.service.Name)
			t.cluster2.ensureNoEndpointSlice(ctx)

			By("Creating namespace on importing cluster")

			createNamespace(ctx, t.cluster2.localDynClient, t.cluster1.service.Namespace)

			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	Specify("an EndpointSlice with a restricted service namespace should not be synced from the broker", func(ctx context.Context) {
		restrictedNamespace := metav1.NamespaceSystem

		test.CreateResource(ctx, endpointSliceClientFor(t.syncerConfig.BrokerClient, test.RemoteNamespace),
			&discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
				Name: "restricted-eps",
				Labels: map[string]string{
					discovery.LabelManagedBy:       constants.LabelValueManagedBy,
					constants.LabelSourceNamespace: restrictedNamespace,
					mcsv1b1.LabelSourceCluster:     "south",
					mcsv1b1.LabelServiceName:       serviceName,
					federate.ClusterIDLabelKey:     "south",
				},
			}})

		testutil.EnsureNoResource(ctx, resource.ForDynamic(endpointSliceClientFor(t.cluster1.localDynClient,
			restrictedNamespace)), "restricted-eps")
	})
}

//nolint:maintidx // This function composes test cases so ignore low maintainability index.
func testClusterIPServiceInTwoClusters() {
	noConflictCondition := mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionConflict, metav1.ConditionFalse,
		mcsv1b1.ServiceExportReasonNoConflicts, "")

	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.cluster1.start(ctx, t, *t.syncerConfig)

		t.cluster1.createServiceEndpointSlices(ctx)
		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)

		t.cluster2.start(ctx, t, *t.syncerConfig)

		t.cluster2.createServiceEndpointSlices(ctx)
		t.cluster2.createService(ctx)
		t.cluster2.createServiceExport(ctx)

		if t.aggregatedIPFamilies == nil {
			t.aggregatedIPFamilies = t.cluster1.service.Spec.IPFamilies
		}
	})

	AfterEach(func() {
		t.afterEach()
	})

	Context("", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(10))},
			}

			t.cluster2.service.Spec.SessionAffinity = t.cluster1.service.Spec.SessionAffinity
			t.cluster2.service.Spec.SessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig

			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
			t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
		})

		It("should export the service in both clusters", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
			t.cluster1.ensureLastServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionTrue,
				mcsv1b1.ServiceExportReasonExported))
			t.cluster1.ensureLastServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionTrue,
				mcsv1b1.ServiceExportReasonValid))
			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
			t.cluster2.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)

			By("Ensure conflict checking does not try to unnecessarily update the ServiceExport status")

			t.cluster1.ensureNoServiceExportActions()
		})
	})

	Context("with differing ports", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.Ports = []corev1.ServicePort{toServicePort(port1), toServicePort(port3)}
			t.aggregatedServicePorts = []mcsv1b1.ServicePort{port1, port2, port3}
		})

		It("should correctly set the ports in the aggregated ServiceImport and "+
			"set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

			condition := newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonPortConflict)
			t.cluster1.awaitServiceExportCondition(ctx, condition)
			t.cluster2.awaitServiceExportCondition(ctx, condition)
		})

		Context("and after unexporting from one cluster", func() {
			It("should correctly update the ports in the aggregated ServiceImport and clear the Conflict status condition",
				func(ctx context.Context) {
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

					t.aggregatedServicePorts = []mcsv1b1.ServicePort{port1, port3}
					t.cluster1.deleteServiceExport(ctx)

					t.awaitNoEndpointSlice(ctx, &t.cluster1)
					t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster2.service.Name, t.cluster2.service.Namespace,
						&t.cluster2)
					t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
				})
		})

		Context("initially and after updating the ports to match", func() {
			It("should correctly update the ports in the aggregated ServiceImport and clear the Conflict status condition",
				func(ctx context.Context) {
					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
					t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonPortConflict))

					t.aggregatedServicePorts = []mcsv1b1.ServicePort{port1, port2}
					t.cluster2.service.Spec.Ports = []corev1.ServicePort{toServicePort(port1), toServicePort(port2)}
					t.cluster2.updateService(ctx)

					t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
					t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
					t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
				})
		})
	})

	Context("with conflicting ports", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.Ports = []corev1.ServicePort{t.cluster1.service.Spec.Ports[0], toServicePort(port3)}
			t.cluster2.service.Spec.Ports[0].Port++
			t.aggregatedServicePorts = []mcsv1b1.ServicePort{port1, port2, port3}
		})

		It("should correctly set the ports in the aggregated ServiceImport and "+
			"set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

			condition := newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonPortConflict)
			t.cluster1.awaitServiceExportCondition(ctx, condition)
			t.cluster2.awaitServiceExportCondition(ctx, condition)
		})
	})

	Context("with differing service types", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.ClusterIP = corev1.ClusterIPNone
			t.cluster2.serviceExport.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Second * 5))
		})

		It("should set the Conflict status condition on the second cluster and not export it", func(ctx context.Context) {
			t.cluster2.ensureNoEndpointSlice(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonTypeConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse, mcsv1b1.ServiceExportReasonFailed))
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonTypeConflict))
		})

		Context("initially and after updating the service types to match", func() {
			It("should export the service in both clusters", func(ctx context.Context) {
				t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonTypeConflict))

				t.cluster2.service.Spec.ClusterIP = t.cluster2.expectedClusterIPEndpoints[0].Addresses[0]
				t.cluster2.updateService(ctx)

				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
				t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
			})
		})
	})

	Context("with differing service SessionAffinity", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
		})

		It("should resolve the conflict and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonSessionAffinityConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonSessionAffinityConflict))
		})

		Context("initially and after updating the SessionAffinity on the conflicting cluster to match", func() {
			It("should clear the Conflict status condition", func(ctx context.Context) {
				t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonSessionAffinityConflict))

				By("Updating the SessionAffinity on the service")

				t.cluster2.service.Spec.SessionAffinity = t.cluster1.service.Spec.SessionAffinity
				t.cluster2.updateService(ctx)

				t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
				t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
			})
		})

		Context("initially and after updating the SessionAffinity on the oldest exporting cluster to match", func() {
			It("should clear the Conflict status condition", func(ctx context.Context) {
				t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
					mcsv1b1.ServiceExportReasonSessionAffinityConflict))

				By("Updating the SessionAffinity on the service")

				t.cluster1.service.Spec.SessionAffinity = t.cluster2.service.Spec.SessionAffinity
				t.cluster1.updateService(ctx)

				t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
				t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
					&t.cluster1, &t.cluster2)

				t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
				t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
			})
		})

		Context("initially and after the service on the oldest exporting cluster is unexported", func() {
			It("should update the SessionAffinity on the aggregated ServiceImport and clear the Conflict status condition",
				func(ctx context.Context) {
					t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
						mcsv1b1.ServiceExportReasonSessionAffinityConflict))

					By("Unexporting the service")

					t.cluster1.deleteServiceExport(ctx)

					t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
					t.aggregatedSessionAffinity = t.cluster2.service.Spec.SessionAffinity
					t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
						&t.cluster2)
				})
		})
	})

	Context("with differing service SessionAffinityConfig", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.cluster2.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity

			t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(10))},
			}
			t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
		})

		It("should resolve the conflict and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))
		})

		Context("initially and after updating the SessionAffinityConfig on the conflicting cluster to match", func() {
			It("should clear the Conflict status condition", func(ctx context.Context) {
				t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
					mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))

				By("Updating the SessionAffinityConfig on the service")

				t.cluster2.service.Spec.SessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
				t.cluster2.updateService(ctx)

				t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
				t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
			})
		})

		Context("initially and after updating the SessionAffinityConfig on the oldest exporting cluster to match", func() {
			It("should clear the Conflict status condition", func(ctx context.Context) {
				t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
					mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))

				By("Updating the SessionAffinityConfig on the service")

				t.cluster1.service.Spec.SessionAffinityConfig = t.cluster2.service.Spec.SessionAffinityConfig
				t.cluster1.updateService(ctx)

				t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
				t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
					&t.cluster1, &t.cluster2)

				t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
				t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
			})
		})

		Context("initially and after the service on the oldest exporting cluster is unexported", func() {
			BeforeEach(func() {
				t.cluster2.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
					ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(20))},
				}
			})

			It("should update the SessionAffinity on the aggregated ServiceImport and clear the Conflict status condition",
				func(ctx context.Context) {
					t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
						mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))

					By("Unexporting the service")

					t.cluster1.deleteServiceExport(ctx)

					t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
					t.aggregatedSessionAffinityConfig = t.cluster2.service.Spec.SessionAffinityConfig
					t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
						&t.cluster2)
				})
		})
	})

	Context("with differing service SessionAffinity and SessionAffinityConfig", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
			t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity

			t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(10))},
			}
			t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig
		})

		It("should resolve the conflicts and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonSessionAffinityConflict,
				mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonSessionAffinityConflict,
				mcsv1b1.ServiceExportReasonSessionAffinityConfigConflict))
		})
	})

	Context("with differing service InternalTrafficPolicy", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.InternalTrafficPolicy = new(corev1.ServiceInternalTrafficPolicyLocal)
			t.aggregatedInternalTrafficPolicy = t.cluster1.service.Spec.InternalTrafficPolicy
		})

		It("should resolve the conflict and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonInternalTrafficPolicyConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonInternalTrafficPolicyConflict))
		})
	})

	Context("with differing service TrafficDistribution", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.TrafficDistribution = new(corev1.ServiceTrafficDistributionPreferSameNode)
			t.aggregatedTrafficDistribution = t.cluster1.service.Spec.TrafficDistribution
		})

		It("should resolve the conflict and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonTrafficDistributionConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonTrafficDistributionConflict))
		})
	})

	Context("with differing service IP families", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol}
			t.cluster2.service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
			t.aggregatedIPFamilies = append(slices.Clone(t.cluster1.service.Spec.IPFamilies), t.cluster2.service.Spec.IPFamilies...)
		})

		It("should resolve the conflict and set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace,
				&t.cluster1, &t.cluster2)

			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonIPFamilyConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				mcsv1b1.ServiceExportReasonIPFamilyConflict))
		})
	})

	Context("with clusterset IP enabled on the first exporting cluster but not the second", func() {
		BeforeEach(func() {
			t.useClusterSetIP = true
			t.cluster1.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
		})

		JustBeforeEach(func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
		})

		It("should set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				controller.ServiceExportReasonClusterSetIPEnablementConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				controller.ServiceExportReasonClusterSetIPEnablementConflict))

			By("Updating the ServiceExport on the second cluster")

			se, err := t.cluster2.localServiceExportClient().Get(ctx, t.cluster2.serviceExport.Name, metav1.GetOptions{})
			Expect(err).To(Succeed())

			se.SetAnnotations(map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)})
			test.UpdateResource(ctx, t.cluster2.localServiceExportClient(), se)

			t.cluster1.awaitServiceExportCondition(ctx, noConflictCondition)
			t.cluster2.awaitServiceExportCondition(ctx, noConflictCondition)
		})

		It("should not release the allocated clusterset IP until all clusters have unexported", func(ctx context.Context) {
			localSI := getServiceImport(ctx, t.cluster1.localServiceImportClient, t.cluster1.service.Namespace, t.cluster1.service.Name)

			By("Unexporting service on the first cluster")

			t.cluster1.deleteServiceExport(ctx)

			t.awaitNoEndpointSlice(ctx, &t.cluster1)
			t.awaitAggregatedServiceImport(ctx, mcsv1b1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster2)

			Consistently(func() error {
				return t.ipPool.Reserve(localSI.Spec.IPs...)
			}).ShouldNot(Succeed(), "ServiceImport IP was released")

			By("Unexporting service on the second cluster")

			t.cluster2.deleteServiceExport(ctx)

			t.awaitServiceUnexported(ctx, &t.cluster2)

			Eventually(func() error {
				return t.ipPool.Reserve(localSI.Spec.IPs...)
			}).Should(Succeed(), "ServiceImport IP was not released")
		})
	})

	Context("with clusterset IP disabled on the first exporting cluster but enabled on the second", func() {
		BeforeEach(func() {
			t.cluster2.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
		})

		It("should set the Conflict status condition on all exporting clusters", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				controller.ServiceExportReasonClusterSetIPEnablementConflict))
			t.cluster2.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(
				controller.ServiceExportReasonClusterSetIPEnablementConflict))
		})
	})

	Context("with clusterset IP enabled on both clusters", func() {
		BeforeEach(func() {
			t.useClusterSetIP = true
			t.cluster1.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
			t.cluster2.serviceExport.Annotations = map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)}
		})

		Specify("the first cluster should allocate the clusterset IP", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

			localSI := getServiceImport(ctx, t.cluster1.localServiceImportClient, t.cluster1.service.Namespace, t.cluster1.service.Name)
			Expect(localSI.Annotations).To(HaveKeyWithValue(constants.ClustersetIPAllocatedBy, t.cluster1.clusterID))

			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
			t.cluster2.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
		})

		Context("with differing ports", func() {
			BeforeEach(func() {
				t.cluster2.service.Spec.Ports = []corev1.ServicePort{toServicePort(port1), toServicePort(port3)}
				t.aggregatedServicePorts = []mcsv1b1.ServicePort{port1, port2, port3}
			})

			It("should correctly set the Conflict status condition", func(ctx context.Context) {
				t.awaitNonHeadlessServiceExported(ctx, &t.cluster1, &t.cluster2)

				t.cluster1.awaitServiceExportCondition(ctx, newServiceExportConflictCondition(mcsv1b1.ServiceExportReasonPortConflict))

				Expect(t.cluster1.retrieveServiceExportCondition(ctx, t.cluster1.serviceExport, mcsv1b1.ServiceExportConditionConflict).
					Message).To(ContainSubstring("expose the union"))
			})
		})
	})
}

func testClusterIPServiceWithMultipleEPS() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)

		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	Specify("the exported EndpointSlice should be correctly updated as backend service EndpointSlices are created/updated/deleted",
		func(ctx context.Context) {
			By("Creating initial service EndpointSlice with no ready endpoints")

			t.cluster1.expectedClusterIPEndpoints[0].Conditions = discovery.EndpointConditions{Ready: new(false)}

			t.cluster1.serviceEndpointSlices[0].Endpoints = []discovery.Endpoint{
				{
					Addresses:  []string{epIP1},
					Conditions: discovery.EndpointConditions{Ready: new(false)},
				},
			}

			t.cluster1.createServiceEndpointSlices(ctx)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			By("Creating service EndpointSlice with ready endpoint")

			t.cluster1.expectedClusterIPEndpoints[0].Conditions = discovery.EndpointConditions{Ready: new(true)}

			t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices, discovery.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:   fmt.Sprintf("%s-%s2", serviceName, clusterID1),
					Labels: t.cluster1.serviceEndpointSlices[0].Labels,
				},
				AddressType: discovery.AddressTypeIPv4,
				Endpoints: []discovery.Endpoint{
					{
						Addresses:  []string{epIP2},
						Conditions: discovery.EndpointConditions{Ready: new(true)},
					},
				},
			})

			t.cluster1.createServiceEndpointSlices(ctx)
			t.ensureEndpointSlice(ctx, &t.cluster1)

			By("Deleting service EndpointSlice with ready endpoint")

			t.cluster1.deleteEndpointSlice(ctx, t.cluster1.serviceEndpointSlices[1].Name)

			t.cluster1.expectedClusterIPEndpoints[0].Conditions = discovery.EndpointConditions{Ready: new(false)}
			t.cluster1.serviceEndpointSlices = t.cluster1.serviceEndpointSlices[:1]

			t.ensureEndpointSlice(ctx, &t.cluster1)
		})
}
