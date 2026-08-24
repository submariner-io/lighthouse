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
	ConfigKeyImportNamespaceDenyList = "import-namespace-deny-list"
	DefaultImportNamespaceDenyList   = "kube-,openshift-,openshift"
)

// NamespaceValidator validates broker-supplied namespace labels against a configurable denylist.
type NamespaceValidator struct {
	denyList []string
}

// NewNamespaceValidator creates a validator using the configured denylist from ConfigMap.
// If not configured, uses DefaultImportNamespaceDenyList.
func NewNamespaceValidator() *NamespaceValidator {
	denyListStr := global.Get(ConfigKeyImportNamespaceDenyList, DefaultImportNamespaceDenyList)

	var denyList []string
	if denyListStr != "" {
		denyList = strings.Split(denyListStr, ",")
		// Trim whitespace from each entry
		for i := range denyList {
			denyList[i] = strings.TrimSpace(denyList[i])
		}
	}

	logger.Infof("Namespace validator using deny list: %v", denyList)

	return &NamespaceValidator{
		denyList: denyList,
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
		if entry == "" {
			continue
		}

		// If entry ends with hyphen, treat as prefix match only, otherwise use exact match.
		if strings.HasSuffix(entry, "-") {
			if strings.HasPrefix(namespace, entry) {
				return errors.Errorf("namespace %q matches denied prefix %q", namespace, entry)
			}
		} else if namespace == entry {
			return errors.Errorf("namespace %q is denied (matches %q)", namespace, entry)
		}
	}

	return nil
}
