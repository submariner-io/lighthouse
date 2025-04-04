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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var _ = Describe("Service EndpointSlice Controller", func() {
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

	When("stopping the previous EPS controller is delayed", func() {
		var fakeEPSClient *epsDynamicClient

		BeforeEach(func() {
			dynClient := dynamicfake.NewSimpleDynamicClient(t.syncerConfig.Scheme)
			fake.AddBasicReactors(&dynClient.Fake)

			fakeEPSClient = &epsDynamicClient{
				dynClient: dynClient,
				epsResourceClient: epsResourceClient{
					ResourceInterface: dynClient.Resource(discovery.SchemeGroupVersion.WithResource(
						"endpointslices")).Namespace(serviceNamespace),
					epsCreateContinue: make(chan any),
					epsCreateStarted:  make(chan any),
				},
			}

			fakeEPSClient.firstCreate.Store(true)

			t.cluster1.init(t.syncerConfig, fakeEPSClient, &dynClient.Fake)

			controller.AwaitStoppedTimeout = time.Millisecond * 100

			DeferCleanup(func() {
				controller.AwaitStoppedTimeout = time.Second * 5
			})
		})

		JustBeforeEach(func() {
			t.cluster1.createService()
			t.cluster1.createServiceExport()
			t.cluster1.createServiceEndpointSlices()
		})

		It("should not create two EndpointSlices after the delay resolves and the new controller is starter", func() {
			Eventually(fakeEPSClient.epsCreateStarted).Within(3 * time.Second).Should(Receive())

			t.cluster1.service.Spec.Ports = append(t.cluster1.service.Spec.Ports, toServicePort(port3))
			t.aggregatedServicePorts = append(t.aggregatedServicePorts, port3)

			By("Updating the service")

			t.cluster1.updateService()

			time.Sleep(time.Millisecond * 500)
			fakeEPSClient.epsCreateContinue <- true

			t.awaitEndpointSlice(&t.cluster1)

			Consistently(func() []*discovery.EndpointSlice {
				return findEndpointSlices(t.cluster1.localEndpointSliceClient, t.cluster1.service.Namespace,
					t.cluster1.service.Name, t.cluster1.clusterID)
			}).Within(time.Millisecond * 500).Should(HaveLen(1))
		})
	})
})

type epsDynamicClient struct {
	dynClient *dynamicfake.FakeDynamicClient
	epsResourceClient
}

func (f *epsDynamicClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	if gvr.Resource == "endpointslices" {
		return &f.epsResourceClient
	}

	return f.dynClient.Resource(gvr)
}

type epsResourceClient struct {
	dynamic.ResourceInterface
	epsCreateContinue chan any
	epsCreateStarted  chan any
	firstCreate       atomic.Bool
}

func (c *epsResourceClient) Namespace(_ string) dynamic.ResourceInterface {
	return c
}

//nolint:gocritic // Ignore hugeParam
func (c *epsResourceClient) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, resources ...string,
) (*unstructured.Unstructured, error) {
	if obj.GetGenerateName() == "" || obj.GetLabels()[discovery.LabelManagedBy] != constants.LabelValueManagedBy {
		return c.ResourceInterface.Create(ctx, obj, opts, resources...)
	}

	if c.firstCreate.CompareAndSwap(true, false) {
		c.epsCreateStarted <- true
		<-c.epsCreateContinue
	}

	return c.ResourceInterface.Create(ctx, obj, opts, resources...)
}
