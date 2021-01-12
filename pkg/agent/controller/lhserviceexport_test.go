/*
© 2020 Red Hat, Inc. and others

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
	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

var _ = Describe("ServiceExport migration", func() {
	var (
		t                     *testDriver
		lhServiceExportClient dynamic.ResourceInterface
	)

	BeforeEach(func() {
		t = newTestDiver()
		lhServiceExportClient = t.cluster1.localDynClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
			&lighthousev2a1.ServiceExport{})).Namespace(serviceNamespace)
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a legacy LH ServiceExport is created", func() {
		It("should export the service and delete the LH ServiceExport", func() {
			serviceExport := &lighthousev2a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      t.service.Name,
					Namespace: t.service.Namespace,
				},
			}

			t.createService()
			test.CreateResource(lhServiceExportClient, serviceExport)
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)

			test.AwaitNoResource(lhServiceExportClient, serviceExport.Name)
		})
	})
})
