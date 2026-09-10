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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/global"
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

// NewNamespaceValidator creates a validator using the configured allowlist and denylist from the global Config.
// If not configured, uses DefaultImportNamespaceDenyList and DefaultImportNamespaceAllowList.
func NewNamespaceValidator() *NamespaceValidator {
	allowList := parseListStr(global.Get(ConfigKeyImportNamespaceAllowList, DefaultImportNamespaceAllowList))
	denyList := parseListStr(global.Get(ConfigKeyImportNamespaceDenyList, DefaultImportNamespaceDenyList))

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
