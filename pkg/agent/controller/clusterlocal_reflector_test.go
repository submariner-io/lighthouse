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
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
)

var _ = Describe("ClusterLocal reflection", func() {
	var (
		t             *testDriver
		reflectedName string
		svcClient     dynamic.ResourceInterface
	)

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
		reflectedName = serviceName + "-" + clusterID1 // nginx-east
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
		svcClient = t.cluster2.localDynClient.Resource(controller.ServiceGVR).Namespace(serviceNamespace)
	})

	AfterEach(func() {
		t.afterEach()
	})

	// Export an nginx Service from cluster1 (east); its EndpointSlice is synced to
	// the broker and down into cluster2 (west), where the reflector consumes it.
	exportFromCluster1 := func(ctx context.Context) {
		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)
		t.cluster1.createServiceEndpointSlices(ctx)
		t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
	}

	awaitReflectedService := func(ctx context.Context) *corev1.Service {
		obj := test.AwaitResource(ctx, svcClient, reflectedName)
		svc := &corev1.Service{}
		Expect(scheme.Scheme.Convert(obj, svc, nil)).To(Succeed())

		return svc
	}

	When("reflection is enabled on the consuming cluster", func() {
		BeforeEach(func() {
			t.cluster2.agentSpec.ReflectClusterLocal = true
		})

		It("should create a native cluster.local Service and EndpointSlice for the import", func(ctx context.Context) {
			exportFromCluster1(ctx)

			By("Verifying the reflected headless Service")

			svc := awaitReflectedService(ctx)
			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Labels).To(HaveKeyWithValue(controller.ReflectorManagedByLabel, controller.ReflectorManagedByValue))
			Expect(svc.Spec.Ports).ToNot(BeEmpty())

			By("Verifying the reflected EndpointSlice")

			Eventually(func(g Gomega, ctx context.Context) {
				slices := findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", reflectedName, "")
				g.Expect(slices).NotTo(BeEmpty())
				g.Expect(slices[0].Labels).To(HaveKeyWithValue(controller.ReflectorManagedByLabel, controller.ReflectorManagedByValue))
				// For a ClusterSetIP import, the reflected slice exposes the clusterset
				// VIP (which Submariner routes), not the remote pod IPs.
				g.Expect(slices[0].Endpoints).To(ContainElement(HaveField("Addresses", ContainElement(ipV4ServiceIP1))))
			}).Within(5 * time.Second).WithContext(ctx).Should(Succeed())
		})

		It("should prune the reflected Service and EndpointSlice when the export is removed", func(ctx context.Context) {
			exportFromCluster1(ctx)
			awaitReflectedService(ctx)

			By("Unexporting the service in cluster1")

			t.cluster1.deleteServiceExport(ctx)
			t.cluster1.deleteEndpointSlice(ctx, t.cluster1.serviceEndpointSlices[0].Name)

			By("Verifying the reflected Service and EndpointSlice are removed")

			Eventually(func(g Gomega, ctx context.Context) {
				_, err := svcClient.Get(ctx, reflectedName, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				g.Expect(findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", reflectedName, "")).To(BeEmpty())
			}).Within(5 * time.Second).WithContext(ctx).Should(Succeed())
		})

		It("should clean up the reflected EndpointSlices when the reflected Service is deleted", func(ctx context.Context) {
			exportFromCluster1(ctx)
			awaitReflectedService(ctx)

			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", reflectedName, "")).ToNot(BeEmpty())
			}).Within(5 * time.Second).WithContext(ctx).Should(Succeed())

			By("Deleting the reflected Service out from under the reflector")

			Expect(svcClient.Delete(ctx, reflectedName, metav1.DeleteOptions{})).To(Succeed())

			By("Verifying the orphaned reflected EndpointSlices are cleaned up")

			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", reflectedName, "")).To(BeEmpty())
			}).Within(5 * time.Second).WithContext(ctx).Should(Succeed())
		})
	})

	When("reflection is disabled (default)", func() {
		It("should not create any cluster.local Service for the import", func(ctx context.Context) {
			exportFromCluster1(ctx)

			By("Waiting for the imported EndpointSlice to arrive in the consuming cluster")

			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", "", clusterID1)).NotTo(BeEmpty())
			}).Within(5 * time.Second).WithContext(ctx).Should(Succeed())

			By("Ensuring no reflected Service is created")

			Consistently(func(g Gomega, ctx context.Context) {
				_, err := svcClient.Get(ctx, reflectedName, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				g.Expect(findEndpointSlices(ctx, t.cluster2.localEndpointSliceClient, "", reflectedName, "")).To(BeEmpty())
			}).WithContext(ctx).WithTimeout(time.Second).Should(Succeed())
		})
	})
})
