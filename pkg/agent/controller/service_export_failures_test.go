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
	"errors"

	. "github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Service export failures", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
		t.createEndpoints()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("Endpoints retrieval initially fails", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
			t.cluster1.endpointsReactor.SetFailOnGet(errors.New("fake Get error"))
		})

		It("should update the ServiceExport status and eventually sync a ServiceImport", func() {
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionUnknown, "ServiceRetrievalFailed"))
			t.cluster1.endpointsReactor.SetResetOnFailure(true)
			t.awaitHeadlessServiceImport("")
		})
	})

	When("a conflict initially occurs when updating the ServiceExport status", func() {
		BeforeEach(func() {
			t.cluster1.localServiceExportClient.FailOnUpdate = apierrors.NewConflict(schema.GroupResource{}, t.serviceExport.Name,
				errors.New("fake conflict"))
		})

		It("should eventually update the ServiceExport status", func() {
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)
		})
	})
})
