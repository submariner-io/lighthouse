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

package controller

import (
	"context"
	"reflect"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/lighthouse/pkg/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (c *ServiceExportClient) removeStatusCondition(name, namespace string, condType mcsv1a1.ServiceExportConditionType, reason string) {
	c.doUpdate(name, namespace, func(toUpdate *mcsv1a1.ServiceExport) bool {
		condition := FindServiceExportStatusCondition(toUpdate.Status.Conditions, condType)
		if condition != nil && reflect.DeepEqual(condition.Reason, &reason) {
			logger.V(log.DEBUG).Infof("Removing status condition (Type: %q, Reason: %q) from ServiceExport (%s/%s)",
				condType, reason, namespace, name)

			toUpdate.Status.Conditions, _ = slices.Remove(toUpdate.Status.Conditions, *condition,
				func(c mcsv1a1.ServiceExportCondition) mcsv1a1.ServiceExportConditionType {
					return c.Type
				})

			return true
		}

		return false
	})
}

func (c *ServiceExportClient) updateStatusConditions(name, namespace string, conditions ...mcsv1a1.ServiceExportCondition) {
	c.tryUpdateStatusConditions(name, namespace, true, conditions...)
}

func (c *ServiceExportClient) tryUpdateStatusConditions(name, namespace string, canReplace bool,
	conditions ...mcsv1a1.ServiceExportCondition,
) {
	findStatusCondition := func(conditions []mcsv1a1.ServiceExportCondition, condType mcsv1a1.ServiceExportConditionType,
	) *mcsv1a1.ServiceExportCondition {
		cond := FindServiceExportStatusCondition(conditions, condType)

		// TODO - this handles migration of the Synced type to Ready which can be removed once we no longer support a version
		// prior to the introduction of Ready.
		if cond == nil && condType == constants.ServiceExportReady {
			cond = FindServiceExportStatusCondition(conditions, "Synced")
		}

		return cond
	}

	c.doUpdate(name, namespace, func(toUpdate *mcsv1a1.ServiceExport) bool {
		updated := false

		for i := range conditions {
			condition := &conditions[i]

			prevCond := findStatusCondition(toUpdate.Status.Conditions, condition.Type)
			if prevCond == nil {
				logger.V(log.DEBUG).Infof("Add status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
					namespace, name, condition.Type, condition.Status, *condition.Reason, *condition.Message)

				toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, *condition)
				updated = true
			} else if serviceExportConditionEqual(prevCond, condition) {
				logger.V(log.TRACE).Infof("Last ServiceExportCondition for (%s/%s) is equal - not updating status: %#v",
					namespace, name, prevCond)
			} else if canReplace {
				logger.V(log.DEBUG).Infof("Update status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
					namespace, name, condition.Type, condition.Status, *condition.Reason, *condition.Message)

				*prevCond = *condition
				updated = true
			}
		}

		return updated
	})
}

func (c *ServiceExportClient) doUpdate(name, namespace string, update func(toUpdate *mcsv1a1.ServiceExport) bool) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := c.Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			logger.Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return errors.Wrap(err, "error retrieving ServiceExport")
		}

		toUpdate := c.toServiceExport(obj)

		updated := update(toUpdate)
		if !updated {
			return nil
		}

		_, err = c.Namespace(toUpdate.Namespace).UpdateStatus(context.TODO(),
			c.toUnstructured(toUpdate), metav1.UpdateOptions{})

		return errors.Wrap(err, "error from UpdateStatus")
	})
	if err != nil {
		logger.Errorf(err, "Error updating status for ServiceExport (%s/%s)", namespace, name)
	}
}

func serviceExportConditionEqual(c1, c2 *mcsv1a1.ServiceExportCondition) bool {
	return c1.Type == c2.Type && c1.Status == c2.Status && reflect.DeepEqual(c1.Reason, c2.Reason) &&
		reflect.DeepEqual(c1.Message, c2.Message)
}
