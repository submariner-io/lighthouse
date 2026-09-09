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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

const (
	ConfigKeyImportNamespaceAllowList = "import-namespace-allow-list"
	ConfigKeyImportNamespaceDenyList  = "import-namespace-deny-list"
	DefaultImportNamespaceDenyList    = "kube-,openshift-,openshift"
	DefaultImportNamespaceAllowList   = "openshift-storage"
)

// NamespaceValidator validates broker-supplied namespace labels against a configurable allowlist and denylist.
type NamespaceValidator struct {
	allowList []string
	denyList  []string
}

// NewNamespaceValidator creates a validator using the configured allowlist and denylist from ConfigMap.
// If not configured, uses DefaultImportNamespaceDenyList and DefaultImportNamespaceAllowList.
func NewNamespaceValidator(dynClient dynamic.Interface) *NamespaceValidator {
	denyListStr := DefaultImportNamespaceDenyList
	allowListStr := DefaultImportNamespaceAllowList

	obj, err := dynClient.Resource(corev1.SchemeGroupVersion.WithResource("configmaps")).Namespace("submariner-operator").Get(
		context.TODO(), "submariner-lighthouse-agent", metav1.GetOptions{})
	if err == nil {
		cm := resource.MustFromUnstructured(obj, &corev1.ConfigMap{})

		s := cm.Data[ConfigKeyImportNamespaceDenyList]
		if s != "" {
			denyListStr = s
		}

		s = cm.Data[ConfigKeyImportNamespaceAllowList]
		if s != "" {
			allowListStr = s
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Failed to get submariner-lighthouse-agent configmap")
	}

	denyList := parseListStr(denyListStr)
	allowList := parseListStr(allowListStr)

	logger.Infof("Namespace validator using allow list: %v, deny list: %v", allowList, denyList)

	return &NamespaceValidator{
		allowList: allowList,
		denyList:  denyList,
	}
}

// CheckAllowed checks if a broker-supplied namespace is safe to use as a target namespace
// in the local cluster. Broker objects are written by remote member-cluster ServiceAccounts
// and are untrusted; allowing them to target privileged or system namespaces enables
// namespace injection attacks (CWE-441: Confused Deputy) where a compromised peer can
// pollute system namespaces fleet-wide, bypassing local ServiceExport admission policies.
func (v *NamespaceValidator) CheckAllowed(namespace string) error {
	// Reject empty namespace explicitly
	if namespace == "" {
		return errors.New("namespace cannot be empty")
	}

	// Check against denylist
	for _, entry := range v.denyList {
		// If entry ends with hyphen, treat as prefix match only, otherwise use exact match.
		if strings.HasSuffix(entry, "-") {
			if strings.HasPrefix(namespace, entry) {
				// Check if allow list overrides this denial
				if v.isInAllowList(namespace) {
					return nil
				}

				return errors.Errorf("namespace %q matches denied prefix %q", namespace, entry)
			}
		} else if namespace == entry {
			// Check if allow list overrides this denial
			if v.isInAllowList(namespace) {
				return nil
			}

			return errors.Errorf("namespace %q is denied (matches %q)", namespace, entry)
		}
	}

	return nil
}

func (v *NamespaceValidator) isInAllowList(namespace string) bool {
	for _, entry := range v.allowList {
		// If entry ends with hyphen, treat as prefix match only, otherwise use exact match.
		if strings.HasSuffix(entry, "-") {
			if strings.HasPrefix(namespace, entry) {
				return true
			}
		} else if namespace == entry {
			return true
		}
	}

	return false
}

func parseListStr(listStr string) []string {
	var retList []string

	if listStr != "" {
		list := strings.Split(listStr, ",")
		retList = make([]string, 0, len(list))

		// Trim whitespace from each entry
		for i := range list {
			s := strings.TrimSpace(list[i])
			if s != "" {
				retList = append(retList, s)
			}
		}
	}

	return retList
}
