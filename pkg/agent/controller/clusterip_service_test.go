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
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("ClusterIP Service export", func() {
	Describe("in single cluster", testClusterIPServiceInOneCluster)
	Describe("in two clusters", testClusterIPServiceInTwoClusters)
	Describe("with multiple service EndpointSlices", testClusterIPServiceWithMultipleEPS)
})

func testClusterIPServiceInOneCluster() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()

		t.cluster1.createServiceEndpointSlices()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		Context("and the Service already exists", func() {
			It("should export the service and update the ServiceExport status", func() {
				t.cluster1.createService()
				t.cluster1.createServiceExport()
				t.awaitNonHeadlessServiceExported(&t.cluster1)
				t.cluster1.awaitServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionFalse, "AwaitingExport"),
					newServiceExportReadyCondition(corev1.ConditionTrue, ""))
				t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)

				By(fmt.Sprintf("Ensure cluster %q does not try to update the status for a non-existent ServiceExport",
					t.cluster2.clusterID))

				t.cluster2.ensureNoServiceExportActions()
			})
		})

		Context("and the Service doesn't initially exist", func() {
			It("should eventually export the service", func() {
				t.cluster1.createServiceExport()
				t.cluster1.awaitServiceUnavailableStatus()

				t.cluster1.createService()
				t.awaitNonHeadlessServiceExported(&t.cluster1)
			})
		})
	})

	When("a ServiceExport is deleted after the service is exported", func() {
		It("should unexport the service", func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			t.cluster1.deleteServiceExport()
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("an exported Service is deleted and recreated while the ServiceExport still exists", func() {
		It("should unexport and re-export the service", func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			t.cluster1.localDynClient.Fake.ClearActions()

			By("Deleting the service")
			t.cluster1.deleteService()
			t.cluster1.awaitServiceUnavailableStatus()
			t.cluster1.awaitServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionFalse, "NoServiceImport"))
			t.awaitServiceUnexported(&t.cluster1)

			By("Re-creating the service")
			t.cluster1.createService()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("the type of an exported Service is updated to an unsupported type", func() {
		It("should unexport the ServiceImport and update the ServiceExport status appropriately", func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			t.cluster1.service.Spec.Type = corev1.ServiceTypeNodePort
			t.cluster1.updateService()

			t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))
			t.cluster1.awaitServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionFalse, "NoServiceImport"))
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a ServiceExport is created for a Service whose type is unsupported", func() {
		BeforeEach(func() {
			t.cluster1.service.Spec.Type = corev1.ServiceTypeNodePort
		})

		JustBeforeEach(func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
		})

		It("should update the ServiceExport status appropriately and not export the serviceImport", func() {
			t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))
			t.cluster1.ensureNoServiceExportCondition(constants.ServiceExportReady)
		})

		Context("and is subsequently updated to a supported type", func() {
			It("should eventually export the service and update the ServiceExport status appropriately", func() {
				t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "UnsupportedServiceType"))

				t.cluster1.service.Spec.Type = corev1.ServiceTypeClusterIP
				t.cluster1.updateService()

				t.awaitNonHeadlessServiceExported(&t.cluster1)
			})
		})
	})

	When("the backend service EndpointSlice has no ready addresses", func() {
		JustBeforeEach(func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})

		Specify("the exported EndpointSlice's service IP address should indicate not ready", func() {
			for i := range t.cluster1.serviceEndpointSlices[0].Endpoints {
				t.cluster1.serviceEndpointSlices[0].Endpoints[i].Conditions = discovery.EndpointConditions{Ready: ptr.To(false)}
			}

			t.cluster1.hasReadyEndpoints = false

			t.cluster1.updateServiceEndpointSlices()
			t.ensureEndpointSlice(&t.cluster1)
		})
	})

	When("the ports for an exported service are updated", func() {
		JustBeforeEach(func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})

		It("should re-export the service with the updated ports", func() {
			t.cluster1.service.Spec.Ports = append(t.cluster1.service.Spec.Ports, toServicePort(port3))
			t.aggregatedServicePorts = append(t.aggregatedServicePorts, port3)

			t.cluster1.updateService()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("two Services with the same name in different namespaces are exported", func() {
		It("should correctly export both services", func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      t.cluster1.service.Name,
					Namespace: "other-service-ns",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "10.253.9.2",
				},
			}

			serviceExport := &mcsv1a1.ServiceExport{
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

			expServiceImport := &mcsv1a1.ServiceImport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
				Spec: mcsv1a1.ServiceImportSpec{
					Type:  mcsv1a1.ClusterSetIP,
					Ports: []mcsv1a1.ServicePort{},
				},
				Status: mcsv1a1.ServiceImportStatus{
					Clusters: []mcsv1a1.ClusterStatus{
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
						discovery.LabelManagedBy:        constants.LabelValueManagedBy,
						constants.MCSLabelSourceCluster: t.cluster1.clusterID,
						mcsv1a1.LabelServiceName:        service.Name,
						constants.LabelSourceNamespace:  service.Namespace,
						constants.LabelIsHeadless:       strconv.FormatBool(false),
					},
				},
				AddressType: discovery.AddressTypeIPv4,
				Endpoints: []discovery.Endpoint{
					{
						Addresses:  []string{service.Spec.ClusterIP},
						Conditions: discovery.EndpointConditions{Ready: ptr.To(false)},
					},
				},
			}

			test.CreateResource(endpointSliceClientFor(t.cluster1.localDynClient, service.Namespace), serviceEPS)
			test.CreateResource(t.cluster1.dynamicServiceClientFor().Namespace(service.Namespace), service)
			test.CreateResource(serviceExportClientFor(t.cluster1.localDynClient, service.Namespace), serviceExport)

			awaitServiceImport(t.cluster2.localServiceImportClient, expServiceImport)
			awaitEndpointSlice(endpointSliceClientFor(t.cluster2.localDynClient, service.Namespace), service.Name, expEndpointSlice)

			// Ensure the resources for the first Service weren't overwritten
			t.awaitAggregatedServiceImport(mcsv1a1.ClusterSetIP, t.cluster1.service.Name, t.cluster1.service.Namespace, &t.cluster1)

			t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict, serviceExport)
		})
	})

	Specify("an EndpointSlice not managed by Lighthouse should not be synced to the broker", func() {
		test.CreateResource(endpointSliceClientFor(t.cluster1.localDynClient, t.cluster1.service.Namespace),
			&discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
				Name:   "other-eps",
				Labels: map[string]string{discovery.LabelManagedBy: "other"},
			}})

		testutil.EnsureNoResource(resource.ForDynamic(endpointSliceClientFor(t.syncerConfig.BrokerClient,
			test.RemoteNamespace)), "other-eps")
	})

	When("an existing ServiceExport has the legacy Synced status condition", func() {
		BeforeEach(func() {
			t.cluster1.serviceExport.Status.Conditions = []mcsv1a1.ServiceExportCondition{
				{
					Type:    "Synced",
					Status:  corev1.ConditionTrue,
					Reason:  ptr.To(""),
					Message: ptr.To("Service was successfully exported to the broker"),
				},
			}
		})

		It("should be migrated to the Ready status condition", func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			t.cluster1.ensureNoServiceExportCondition("Synced")
		})
	})
}

func testClusterIPServiceInTwoClusters() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.cluster1.createServiceEndpointSlices()
		t.cluster1.createService()
		t.cluster1.createServiceExport()

		t.justBeforeEach()

		t.cluster2.createServiceEndpointSlices()
		t.cluster2.createService()
		t.cluster2.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	It("should export the service in both clusters", func() {
		t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)
		t.cluster1.ensureLastServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionTrue, ""))
		t.cluster1.ensureLastServiceExportCondition(newServiceExportValidCondition(corev1.ConditionTrue, ""))
		t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
		t.cluster2.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)

		By("Ensure conflict checking does not try to unnecessarily update the ServiceExport status")

		t.cluster1.ensureNoServiceExportActions()
	})

	Context("with differing ports", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.Ports = []corev1.ServicePort{toServicePort(port1), toServicePort(port3)}
			t.aggregatedServicePorts = []mcsv1a1.ServicePort{port1, port2, port3}
		})

		It("should correctly set the ports in the aggregated ServiceImport and set the Conflict status condition", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)

			condition := newServiceExportConflictCondition("ConflictingPorts")
			t.cluster1.awaitServiceExportCondition(condition)
			t.cluster2.awaitServiceExportCondition(condition)
		})

		Context("and after unexporting from one cluster", func() {
			It("should correctly update the ports in the aggregated ServiceImport and clear the Conflict status condition", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)

				t.aggregatedServicePorts = []mcsv1a1.ServicePort{port1, port3}
				t.cluster1.deleteServiceExport()

				t.awaitNoEndpointSlice(&t.cluster1)
				t.awaitAggregatedServiceImport(mcsv1a1.ClusterSetIP, t.cluster2.service.Name, t.cluster2.service.Namespace, &t.cluster2)
				t.cluster2.awaitNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			})
		})

		Context("initially and after updating the ports to match", func() {
			It("should correctly update the ports in the aggregated ServiceImport and clear the Conflict status condition", func() {
				t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)
				t.cluster1.awaitServiceExportCondition(newServiceExportConflictCondition("ConflictingPorts"))

				t.aggregatedServicePorts = []mcsv1a1.ServicePort{port1, port2}
				t.cluster2.service.Spec.Ports = []corev1.ServicePort{toServicePort(port1), toServicePort(port2)}
				t.cluster2.updateService()

				t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)
				t.cluster1.awaitNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
				t.cluster2.awaitNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			})
		})
	})

	Context("with differing service types", func() {
		BeforeEach(func() {
			t.cluster2.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		It("should set the Conflict status condition on the second cluster and not export it", func() {
			t.cluster2.ensureNoEndpointSlice()
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			t.cluster2.awaitServiceExportCondition(newServiceExportConflictCondition("ConflictingType"))
			t.cluster2.awaitServiceExportCondition(newServiceExportReadyCondition(corev1.ConditionFalse, "ExportFailed"))
			t.cluster1.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
		})

		Context("initially and after updating the service types to match", func() {
			It("should export the service in both clusters", func() {
				t.cluster2.awaitServiceExportCondition(newServiceExportConflictCondition("ConflictingType"))

				t.cluster2.service.Spec.ClusterIP = t.cluster2.serviceIP
				t.cluster2.updateService()

				t.awaitNonHeadlessServiceExported(&t.cluster1, &t.cluster2)
				t.cluster2.ensureNoServiceExportCondition(mcsv1a1.ServiceExportConflict)
			})
		})
	})
}

func testClusterIPServiceWithMultipleEPS() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()

		t.cluster1.createService()
		t.cluster1.createServiceExport()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	Specify("the exported EndpointSlice should be correctly updated as backend service EndpointSlices are created/updated/deleted", func() {
		By("Creating initial service EndpointSlice with no ready endpoints")

		t.cluster1.hasReadyEndpoints = false
		t.cluster1.serviceEndpointSlices[0].Endpoints = []discovery.Endpoint{
			{
				Addresses:  []string{epIP1},
				Conditions: discovery.EndpointConditions{Ready: ptr.To(false)},
			},
		}

		t.cluster1.createServiceEndpointSlices()
		t.awaitNonHeadlessServiceExported(&t.cluster1)

		By("Creating service EndpointSlice with ready endpoint")

		t.cluster1.hasReadyEndpoints = true
		t.cluster1.serviceEndpointSlices = append(t.cluster1.serviceEndpointSlices, discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:   fmt.Sprintf("%s-%s2", serviceName, clusterID1),
				Labels: t.cluster1.serviceEndpointSlices[0].Labels,
			},
			AddressType: discovery.AddressTypeIPv4,
			Endpoints: []discovery.Endpoint{
				{
					Addresses:  []string{epIP2},
					Conditions: discovery.EndpointConditions{Ready: ptr.To(true)},
				},
			},
		})

		t.cluster1.createServiceEndpointSlices()
		t.ensureEndpointSlice(&t.cluster1)

		By("Deleting service EndpointSlice with ready endpoint")

		t.cluster1.deleteEndpointSlice(t.cluster1.serviceEndpointSlices[1].Name)

		t.cluster1.hasReadyEndpoints = false
		t.cluster1.serviceEndpointSlices = t.cluster1.serviceEndpointSlices[:1]

		t.ensureEndpointSlice(&t.cluster1)
	})
}
