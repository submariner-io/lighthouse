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

	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

var _ = Describe("Pre-clusterset IP ServiceImport migration", func() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		test.CreateResource(ctx, t.brokerServiceImportClient.Namespace(test.RemoteNamespace), &mcsv1b1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace),
				Annotations: map[string]string{
					mcsv1b1.LabelServiceName:       serviceName,
					constants.LabelSourceNamespace: serviceNamespace,
				},
			},
			Spec: mcsv1b1.ServiceImportSpec{
				Type: mcsv1b1.ClusterSetIP,
			},
		})

		t.cluster1.service.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
		t.cluster1.service.Spec.SessionAffinityConfig = &corev1.SessionAffinityConfig{
			ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: ptr.To(int32(10))},
		}

		t.aggregatedSessionAffinity = t.cluster1.service.Spec.SessionAffinity
		t.aggregatedSessionAffinityConfig = t.cluster1.service.Spec.SessionAffinityConfig

		t.cluster1.createService(ctx)
		t.cluster1.createServiceExport(ctx)

		t.justBeforeEach(ctx)

		t.cluster1.createServiceEndpointSlices(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	It("should update the existing aggregated ServiceImport and not create any Conflict conditions", func(ctx context.Context) {
		t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

		t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1b1.ServiceExportConditionConflict)
	})
})
