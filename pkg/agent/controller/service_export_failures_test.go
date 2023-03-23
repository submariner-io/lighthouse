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

	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Service export failures", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.cluster1.createService()
		t.cluster1.createEndpoints()
		t.cluster1.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("the local ServiceImport creation initially fails", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportReactor.SetFailOnCreate(errors.New("mock create error"))
		})

		It("should eventually export the service", func() {
			t.cluster1.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionTrue, ""))
			t.cluster1.ensureNoServiceExportCondition(constants.ServiceExportSynced)

			t.cluster1.localServiceImportReactor.SetFailOnCreate(nil)
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("the aggregated ServiceImport creation initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnCreate(errors.New("mock create error"))
		})

		It("should eventually export the service", func() {
			t.cluster1.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "ExportFailed"))

			t.brokerServiceImportReactor.SetFailOnCreate(nil)
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("the aggregated ServiceImport update initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnUpdate(errors.New("mock update error"))
		})

		It("should eventually export the service", func() {
			t.cluster1.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "ExportFailed"))

			t.brokerServiceImportReactor.SetFailOnUpdate(nil)
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("the aggregated ServiceImport delete initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportReactor.SetFailOnDelete(errors.New("mock delete error"))
			t.brokerServiceImportReactor.SetResetOnFailure(true)
		})

		It("should eventually unexport the service", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
			t.cluster1.deleteServiceExport()
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("a conflict initially occurs when updating the ServiceExport status", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportReactor.SetFailOnUpdate(apierrors.NewConflict(schema.GroupResource{}, t.cluster1.serviceExport.Name,
				errors.New("fake conflict")))
			t.cluster1.localServiceImportReactor.SetResetOnFailure(true)
		})

		It("should eventually update the ServiceExport status", func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})

	When("deleting the local EndpointSlice on unexport initially fails", func() {
		JustBeforeEach(func() {
			t.awaitNonHeadlessServiceExported(&t.cluster1)

			fake.FailOnAction(&t.cluster1.localDynClient.Fake, "endpointslices", "delete-collection", nil, true)
		})

		It("should eventually unexport the service", func() {
			t.cluster1.deleteServiceExport()
			t.awaitServiceUnexported(&t.cluster1)
		})
	})

	When("listing the broker EndpointSlices initially fails", func() {
		BeforeEach(func() {
			t.brokerEndpointSliceReactor.SetFailOnList(errors.New("mock list error"))
		})

		It("should eventually export the service", func() {
			t.cluster1.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "ExportFailed"))

			t.brokerEndpointSliceReactor.SetFailOnList(nil)
			t.awaitNonHeadlessServiceExported(&t.cluster1)
		})
	})
})
