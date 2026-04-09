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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/fake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Service export failures", func() {
	var t *testDriver

	BeforeEach(func(ctx context.Context) {
		t = newTestDiver(ctx)
	})

	JustBeforeEach(func(ctx context.Context) {
		t.justBeforeEach(ctx)
		t.cluster1.createService(ctx)
		t.cluster1.createServiceEndpointSlices(ctx)
		t.cluster1.createServiceExport(ctx)
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("the local ServiceImport creation initially fails", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportReactor.SetFailOnCreate(errors.New("mock create error"))
		})

		It("should eventually export the service", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionTrue, mcsv1a1.ServiceExportReasonValid))
			t.cluster1.ensureNoServiceExportCondition(ctx, mcsv1a1.ServiceExportConditionReady)

			t.cluster1.localServiceImportReactor.SetFailOnCreate(nil)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the aggregated ServiceImport creation initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnCreate(errors.New("mock create error"))
		})

		It("should eventually export the service", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse, mcsv1a1.ServiceExportReasonFailed))

			t.brokerServiceImportReactor.SetFailOnCreate(nil)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the aggregated ServiceImport update initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnUpdate(errors.New("mock update error"))
		})

		It("should eventually export the service", func(ctx context.Context) {
			t.cluster1.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionFalse, mcsv1a1.ServiceExportReasonFailed))

			t.brokerServiceImportReactor.SetFailOnUpdate(nil)
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("the aggregated ServiceImport delete initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnDelete(errors.New("mock delete error"))
			t.brokerServiceImportReactor.SetResetOnFailure(true)
		})

		It("should eventually unexport the service", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
			t.cluster1.deleteServiceExport(ctx)
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})

	When("a conflict initially occurs when updating the ServiceExport status", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportReactor.SetFailOnUpdate(apierrors.NewConflict(schema.GroupResource{}, t.cluster1.serviceExport.Name,
				errors.New("fake conflict")))
			t.cluster1.localServiceImportReactor.SetResetOnFailure(true)
		})

		It("should eventually update the ServiceExport status", func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)
		})
	})

	When("deleting the local EndpointSlice on unexport initially fails", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.awaitNonHeadlessServiceExported(ctx, &t.cluster1)

			fake.FailOnAction(t.cluster1.localDynClientFake, "endpointslices", "delete-collection", nil, true)
		})

		It("should eventually unexport the service", func(ctx context.Context) {
			t.cluster1.deleteServiceExport(ctx)
			t.awaitServiceUnexported(ctx, &t.cluster1)
		})
	})
})
