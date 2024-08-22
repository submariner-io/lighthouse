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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
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
			cond1 := mcsv1a1.ServiceExportCondition{
				Type:    constants.ServiceExportReady,
				Status:  corev1.ConditionFalse,
				Reason:  ptr.To("Failed"),
				Message: ptr.To("A failure occurred"),
			}

			cond2 := mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportValid,
				Status:  corev1.ConditionFalse,
				Reason:  ptr.To("NotValid"),
				Message: ptr.To("Not valid"),
			}

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1, cond2)

			se := getServiceExport()
			Expect(controller.FindServiceExportStatusCondition(se.Status.Conditions, cond1.Type)).To(Equal(&cond1))
			Expect(controller.FindServiceExportStatusCondition(se.Status.Conditions, cond2.Type)).To(Equal(&cond2))

			cond1.Status = corev1.ConditionTrue
			cond1.Reason = ptr.To("")
			cond1.Message = ptr.To("")

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1)

			Expect(controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond1.Type)).To(Equal(&cond1))

			dynClient.ClearActions()
			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond1)

			test.EnsureNoActionsForResource(&dynClient.Fake, "serviceexports", "update")
		})
	})

	Context("RemoveStatusCondition", func() {
		BeforeEach(func() {
			initialServiceExport.Status.Conditions = []mcsv1a1.ServiceExportCondition{
				{
					Type:   constants.ServiceExportReady,
					Status: corev1.ConditionFalse,
					Reason: ptr.To("Failed"),
				},
			}
		})

		It("should remove the condition if the reason matches", func() {
			serviceExportClient.RemoveStatusCondition(context.TODO(), serviceName, serviceNamespace,
				constants.ServiceExportReady, "Failed")

			Expect(controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions,
				constants.ServiceExportReady)).To(BeNil())
		})

		It("should not remove the condition if the reason does not match", func() {
			serviceExportClient.RemoveStatusCondition(context.TODO(), serviceName, serviceNamespace,
				constants.ServiceExportReady, "NotMatching")

			Expect(controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions,
				constants.ServiceExportReady)).ToNot(BeNil())
		})
	})

	Context("with Conflict condition type", func() {
		It("should aggregate the different reasons and messages", func() {
			// The condition shouldn't be added with Status False.

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportConflict,
				Status:  corev1.ConditionFalse,
				Reason:  ptr.To(""),
				Message: ptr.To(""),
			})

			Expect(controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions,
				mcsv1a1.ServiceExportConflict)).To(BeNil())

			portConflictMsg := "The service ports conflict"
			typeConflictMsg := "The service types conflict"

			cond := mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportConflict,
				Status:  corev1.ConditionTrue,
				Reason:  ptr.To(controller.PortConflictReason),
				Message: ptr.To(portConflictMsg),
			}

			// Add first condition reason

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			Expect(controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)).To(Equal(&cond))

			// Add second condition reason

			cond.Reason = ptr.To(controller.TypeConflictReason)
			cond.Message = ptr.To(typeConflictMsg)

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual := controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(strings.Split(*actual.Reason, ",")).To(HaveExactElements(controller.PortConflictReason, controller.TypeConflictReason))
			Expect(strings.Split(*actual.Message, "\n")).To(HaveExactElements(portConflictMsg, typeConflictMsg))
			Expect(actual.Status).To(Equal(corev1.ConditionTrue))

			// Update second condition message

			typeConflictMsg = "The service types still conflict"
			cond.Message = ptr.To(typeConflictMsg)

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual = controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(strings.Split(*actual.Reason, ",")).To(HaveExactElements(controller.PortConflictReason, controller.TypeConflictReason))
			Expect(strings.Split(*actual.Message, "\n")).To(HaveExactElements(portConflictMsg, typeConflictMsg))
			Expect(actual.Status).To(Equal(corev1.ConditionTrue))

			// Resolve first condition

			cond.Reason = ptr.To(controller.PortConflictReason)
			cond.Message = ptr.To("")
			cond.Status = corev1.ConditionFalse

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

			actual = controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(*actual.Reason).To(Equal(controller.TypeConflictReason))
			Expect(*actual.Message).To(Equal(typeConflictMsg))
			Expect(actual.Status).To(Equal(corev1.ConditionTrue))

			// Resolve second condition

			cond.Reason = ptr.To(controller.TypeConflictReason)

			for i := 1; i <= 2; i++ {
				serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, cond)

				actual = controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)
				Expect(actual.Status).To(Equal(corev1.ConditionFalse))
				Expect(*actual.Reason).To(BeEmpty())
				Expect(*actual.Message).To(BeEmpty())
			}

			// Add the first condition back

			serviceExportClient.UpdateStatusConditions(context.TODO(), serviceName, serviceNamespace, mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportConflict,
				Status:  corev1.ConditionTrue,
				Reason:  ptr.To(controller.PortConflictReason),
				Message: ptr.To(""),
			}, mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportConflict,
				Status:  corev1.ConditionFalse,
				Reason:  ptr.To(controller.TypeConflictReason),
				Message: ptr.To(""),
			})

			actual = controller.FindServiceExportStatusCondition(getServiceExport().Status.Conditions, cond.Type)
			Expect(*actual.Reason).To(Equal(controller.PortConflictReason))
			Expect(actual.Status).To(Equal(corev1.ConditionTrue))
		})
	})
})
