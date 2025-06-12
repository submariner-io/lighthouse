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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("ServiceExportClient", func() {
	var (
		serviceExportClient  *controller.ServiceExportClient
		dynClient            *dynamicfake.FakeDynamicClient
		initialServiceExport *mcsv1a1.ServiceExport
	)

	BeforeEach(func() {
		initialServiceExport = &mcsv1a1.ServiceExport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: serviceNamespace,
			},
		}
	})

	JustBeforeEach(func() {
		dynClient = dynamicfake.NewSimpleDynamicClient(scheme.Scheme, initialServiceExport)
		serviceExportClient = controller.NewServiceExportClient(dynClient, scheme.Scheme)
	})

	getServiceExport := func() *mcsv1a1.ServiceExport {
		obj, err := serviceExportClientFor(dynClient, serviceNamespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
		Expect(err).To(Succeed())

		return toServiceExport(obj)
	}

	Context("UpdateStatusConditions", func() {
		It("should correctly add/update conditions", func() {
			cond1 := metav1.Condition{
				Type:    constants.ServiceExportReady,
				Status:  metav1.ConditionFalse,
				Reason:  "Failed",
				Message: "A failure occurred",
			}

			cond2 := metav1.Condition{
				Type:    mcsv1a1.ServiceExportValid,
				Status:  metav1.ConditionFalse,
				Reason:  "NotValid",
				Message: "Not valid",
			}

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1, cond2)

			se := getServiceExport()
			Expect(meta.FindStatusCondition(se.Status.Conditions, cond1.Type)).To(Equal(&cond1))
			Expect(meta.FindStatusCondition(se.Status.Conditions, cond2.Type)).To(Equal(&cond2))

			cond1.Status = metav1.ConditionTrue
			cond1.Reason = ""
			cond1.Message = ""

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1)

			Expect(meta.FindStatusCondition(getServiceExport().Status.Conditions, cond1.Type)).To(Equal(&cond1))

			dynClient.ClearActions()
			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1)

			test.EnsureNoActionsForResource(&dynClient.Fake, "serviceexports", "update")
		})
	})

	Context("with Conflict condition type", func() {
		It("should aggregate the different reasons and messages", func() {
			// The condition shouldn't be added with Status False.

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, metav1.Condition{
				Type:   mcsv1a1.ServiceExportConflict,
				Status: metav1.ConditionFalse,
			})

			Expect(meta.FindStatusCondition(getServiceExport().Status.Conditions,
				mcsv1a1.ServiceExportConflict)).To(BeNil())

			portConflictMsg := "The service ports conflict"
			typeConflictMsg := "The service types conflict"

			cond := metav1.Condition{
				Type:    mcsv1a1.ServiceExportConflict,
				Status:  metav1.ConditionTrue,
				Reason:  controller.PortConflictReason,
				Message: portConflictMsg,
			}

			// Add first condition reason

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			Expect(meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)).To(Equal(&cond))

			// Add second condition reason

			cond.Reason = controller.TypeConflictReason
			cond.Message = typeConflictMsg

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual := meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(strings.Split(actual.Reason, ",")).To(HaveExactElements(controller.PortConflictReason, controller.TypeConflictReason))
			Expect(strings.Split(actual.Message, "\n")).To(HaveExactElements(portConflictMsg, typeConflictMsg))
			Expect(actual.Status).To(Equal(metav1.ConditionTrue))

			// Update second condition message

			typeConflictMsg = "The service types still conflict"
			cond.Message = typeConflictMsg

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual = meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(strings.Split(actual.Reason, ",")).To(HaveExactElements(controller.PortConflictReason, controller.TypeConflictReason))
			Expect(strings.Split(actual.Message, "\n")).To(HaveExactElements(portConflictMsg, typeConflictMsg))
			Expect(actual.Status).To(Equal(metav1.ConditionTrue))

			// Resolve first condition

			cond.Reason = controller.PortConflictReason
			cond.Message = ""
			cond.Status = metav1.ConditionFalse

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual = meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(actual.Reason).To(Equal(controller.TypeConflictReason))
			Expect(actual.Message).To(Equal(typeConflictMsg))
			Expect(actual.Status).To(Equal(metav1.ConditionTrue))

			// Resolve second condition

			cond.Reason = controller.TypeConflictReason

			for i := 1; i <= 2; i++ {
				serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

				actual = meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)
				Expect(actual.Status).To(Equal(metav1.ConditionFalse))
				Expect(actual.Reason).To(Equal(controller.NoConflictsReason))
				Expect(actual.Message).To(BeEmpty())
			}

			// Add the first condition back

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, metav1.Condition{
				Type:   mcsv1a1.ServiceExportConflict,
				Status: metav1.ConditionTrue,
				Reason: controller.PortConflictReason,
			}, metav1.Condition{
				Type:   mcsv1a1.ServiceExportConflict,
				Status: metav1.ConditionFalse,
				Reason: controller.TypeConflictReason,
			})

			actual = meta.FindStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(actual.Reason).To(Equal(controller.PortConflictReason))
			Expect(actual.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})
