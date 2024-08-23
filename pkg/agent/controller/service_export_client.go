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
	goslices "slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func NewServiceExportClient(client dynamic.Interface, scheme *runtime.Scheme) *ServiceExportClient {
	return &ServiceExportClient{
		NamespaceableResourceInterface: client.Resource(schema.GroupVersionResource{
			Group:    mcsv1a1.GroupVersion.Group,
			Version:  mcsv1a1.GroupVersion.Version,
			Resource: "serviceexports",
		}),
		converter: converter{scheme: scheme},
	}
}

func (c *ServiceExportClient) UpdateStatusConditions(ctx context.Context, name, namespace string,
	conditions ...mcsv1a1.ServiceExportCondition,
) {
	c.tryUpdateStatusConditions(ctx, name, namespace, true, conditions...)
}

func (c *ServiceExportClient) tryUpdateStatusConditions(ctx context.Context, name, namespace string, canReplace bool,
	conditions ...mcsv1a1.ServiceExportCondition,
) {
	if len(conditions) == 0 {
		return
	}

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

	c.doUpdate(ctx, name, namespace, func(toUpdate *mcsv1a1.ServiceExport) bool {
		updated := false

		for i := range conditions {
			condition := &conditions[i]

			prevCond := findStatusCondition(toUpdate.Status.Conditions, condition.Type)

			if prevCond == nil {
				if condition.Type == mcsv1a1.ServiceExportConflict && condition.Status == corev1.ConditionFalse {
					// The caller intends to clear the Conflict condition so don't add it.
					continue
				}

				logger.V(log.DEBUG).Infof("Add status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
					namespace, name, condition.Type, condition.Status, *condition.Reason, *condition.Message)

				toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, *condition)
				updated = true
			} else if condition.Type == mcsv1a1.ServiceExportConflict {
				condUpdated := c.mergeConflictCondition(prevCond, condition)
				if condUpdated {
					logger.V(log.DEBUG).Infof(
						"Update status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
						namespace, name, condition.Type, prevCond.Status, *prevCond.Reason, *prevCond.Message)
				}

				updated = updated || condUpdated
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

func (c *ServiceExportClient) mergeConflictCondition(to, from *mcsv1a1.ServiceExportCondition) bool {
	var reasons, messages []string

	if ptr.Deref(to.Reason, "") != "" {
		reasons = strings.Split(*to.Reason, ",")
	}

	if ptr.Deref(to.Message, "") != "" {
		messages = strings.Split(*to.Message, "\n")
	}

	index := goslices.Index(reasons, *from.Reason)
	if index >= 0 {
		if from.Status == corev1.ConditionTrue {
			if index < len(messages) {
				messages[index] = *from.Message
			}
		} else {
			reasons = goslices.Delete(reasons, index, index+1)

			if index < len(messages) {
				messages = goslices.Delete(messages, index, index+1)
			}
		}
	} else if from.Status == corev1.ConditionTrue {
		reasons = append(reasons, *from.Reason)
		messages = append(messages, *from.Message)
	}

	newReason := strings.Join(reasons, ",")
	newMessage := strings.Join(messages, "\n")
	updated := newReason != ptr.Deref(to.Reason, "") || newMessage != ptr.Deref(to.Message, "")

	to.Reason = ptr.To(newReason)
	to.Message = ptr.To(newMessage)

	if *to.Reason != "" {
		to.Status = corev1.ConditionTrue
	} else {
		to.Status = corev1.ConditionFalse
	}

	if updated {
		to.LastTransitionTime = from.LastTransitionTime
	}

	return updated
}

func (c *ServiceExportClient) doUpdate(ctx context.Context, name, namespace string, update func(toUpdate *mcsv1a1.ServiceExport) bool) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := c.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			logger.V(log.TRACE).Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return errors.Wrap(err, "error retrieving ServiceExport")
		}

		toUpdate := c.toServiceExport(obj)

		updated := update(toUpdate)
		if !updated {
			return nil
		}

		_, err = c.Namespace(toUpdate.Namespace).UpdateStatus(ctx, c.toUnstructured(toUpdate), metav1.UpdateOptions{})

		return errors.Wrap(err, "error from UpdateStatus")
	})
	if err != nil {
		logger.Errorf(err, "Error updating status for ServiceExport (%s/%s)", namespace, name)
	}
}

func (c *ServiceExportClient) getLocalInstance(name, namespace string) *mcsv1a1.ServiceExport {
	obj, found, _ := c.localSyncer.GetResource(name, namespace)
	if !found {
		return nil
	}

	return obj.(*mcsv1a1.ServiceExport)
}

//nolint:unparam // Ignore `condType` always receives `mcsv1a1.ServiceExportConflict`
func (c *ServiceExportClient) hasCondition(name, namespace string, condType mcsv1a1.ServiceExportConditionType, reason string) bool {
	se := c.getLocalInstance(name, namespace)
	if se == nil {
		return false
	}

	cond := FindServiceExportStatusCondition(se.Status.Conditions, condType)

	return cond != nil && strings.Contains(ptr.Deref(cond.Reason, ""), reason)
}

func serviceExportConditionEqual(c1, c2 *mcsv1a1.ServiceExportCondition) bool {
	return c1.Type == c2.Type && c1.Status == c2.Status && reflect.DeepEqual(c1.Reason, c2.Reason) &&
		reflect.DeepEqual(c1.Message, c2.Message)
}
