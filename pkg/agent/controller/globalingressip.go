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
	"github.com/submariner-io/admiral/pkg/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

const (
	ClusterIPService         = "ClusterIPService"
	HeadlessServicePod       = "HeadlessServicePod"
	HeadlessServiceEndpoints = "HeadlessServiceEndpoints"
	defaultMsgIPUnavailable  = "Service doesn't have a global IP yet"
)

type IngressIP struct {
	namespace         string
	allocatedIP       string
	unallocatedReason mcsv1b1.ServiceExportConditionReason
	unallocatedMsg    string
}

func parseIngressIP(obj *unstructured.Unstructured) *IngressIP {
	gip := &IngressIP{}
	gip.namespace = obj.GetNamespace()

	gip.allocatedIP, _, _ = unstructured.NestedString(obj.Object, "status", "allocatedIP")
	if gip.allocatedIP == "" {
		gip.unallocatedMsg = defaultMsgIPUnavailable
		gip.unallocatedReason = ServiceExportReasonGlobalIPUnavailable

		conditions := util.ConditionsFromUnstructured(obj, "status", "conditions")
		for i := range conditions {
			if conditions[i].Type == "Allocated" {
				gip.unallocatedMsg = "Unable to obtain global IP: " + conditions[i].Message
				gip.unallocatedReason = mcsv1b1.ServiceExportConditionReason(conditions[i].Reason)

				break
			}
		}
	}

	return gip
}

func GetGlobalIngressIPObj() *unstructured.Unstructured {
	gip := &unstructured.Unstructured{}
	gip.SetKind("GlobalIngressIP")
	gip.SetAPIVersion("submariner.io/v1")

	return gip
}
