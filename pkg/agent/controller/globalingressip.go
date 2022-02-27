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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	ClusterIPService           = "ClusterIPService"
	HeadlessServicePod         = "HeadlessServicePod"
	defaultReasonIPUnavailable = "ServiceGlobalIPUnavailable"
	defaultMsgIPUnavailable    = "Service doesn't have a global IP yet"
)

type IngressIP struct {
	namespace         string
	target            string
	allocatedIP       string
	unallocatedReason string
	unallocatedMsg    string
}

func parseIngressIP(obj *unstructured.Unstructured) *IngressIP {
	var (
		found bool
		err   error
	)

	gip := &IngressIP{}
	gip.namespace = obj.GetNamespace()

	gip.target, found, err = unstructured.NestedString(obj.Object, "spec", "target")
	if !found || err != nil {
		logger.Error(nil, "target field not found in spec", "value", obj.Object)
		return nil
	}

	gip.allocatedIP, _, _ = unstructured.NestedString(obj.Object, "status", "allocatedIP")
	if gip.allocatedIP == "" {
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if len(conditions) > 0 {
			latestCondition := conditions[len(conditions)-1].(map[string]interface{})
			gip.unallocatedMsg = latestCondition["message"].(string)
			gip.unallocatedReason = latestCondition["reason"].(string)
		} else {
			gip.unallocatedMsg = defaultMsgIPUnavailable
			gip.unallocatedReason = defaultReasonIPUnavailable
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
