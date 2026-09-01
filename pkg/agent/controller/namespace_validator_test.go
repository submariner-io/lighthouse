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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("NamespaceValidator", func() {
	var (
		validator *controller.NamespaceValidator
		configMap *corev1.ConfigMap
	)

	BeforeEach(func() {
		configMap = nil
	})

	JustBeforeEach(func() {
		global.Init(configMap)

		DeferCleanup(func() {
			global.Init(nil)
		})

		validator = controller.NewNamespaceValidator()
	})

	Context("with no configured deny list", func() {
		It("should use the default deny list", func() {
			Expect(validator.CheckAllowed("kube-system")).NotTo(Succeed())
			Expect(validator.CheckAllowed("kube-public")).NotTo(Succeed())
			Expect(validator.CheckAllowed("kube-node-lease")).NotTo(Succeed())
			Expect(validator.CheckAllowed("openshift-ovn-kubernetes")).NotTo(Succeed())
			Expect(validator.CheckAllowed("openshift-console")).NotTo(Succeed())
			Expect(validator.CheckAllowed("")).NotTo(Succeed())

			Expect(validator.CheckAllowed("default")).To(Succeed())
			Expect(validator.CheckAllowed("my-app")).To(Succeed())
			Expect(validator.CheckAllowed("production")).To(Succeed())
			Expect(validator.CheckAllowed("my-openshift-app")).To(Succeed())
			Expect(validator.CheckAllowed("test-kube-demo")).To(Succeed())
		})
	})

	Context("with a configured deny list", func() {
		BeforeEach(func() {
			configMap = &corev1.ConfigMap{
				Data: map[string]string{
					controller.ConfigKeyImportNamespaceDenyList: "default , system-",
				},
			}
		})

		It("should use it", func() {
			Expect(validator.CheckAllowed("default")).NotTo(Succeed())
			Expect(validator.CheckAllowed("system-core")).NotTo(Succeed())

			Expect(validator.CheckAllowed("defaulttest")).To(Succeed())
			Expect(validator.CheckAllowed("my-system")).To(Succeed())
		})
	})
})
