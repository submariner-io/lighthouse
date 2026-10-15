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

package framework_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var _ = Describe("Nettest helper Deployments", func() {
	var (
		openShift *fakeCluster
		plain     *fakeCluster
		f         *lhframework.Framework
	)

	BeforeEach(func() {
		openShift = &fakeCluster{openShift: true}
		plain = &fakeCluster{}

		oldClients := framework.KubeClients
		DeferCleanup(func() { framework.KubeClients = oldClients })

		framework.KubeClients = []*kubernetes.Clientset{plain.start(), openShift.start()}

		f = &lhframework.Framework{Framework: framework.NewBareFramework("nettest-uid")}
		f.Namespace = "nettest-uid"
	})

	DescribeTable("should leave the UID to SCC admission only on the cluster that serves security.openshift.io",
		func(build func(*lhframework.Framework, framework.ClusterIndex) *corev1.PodList) {
			build(f, framework.ClusterB)
			build(f, framework.ClusterA)

			Expect(openShift.runAsUsers).To(ConsistOf(BeNil()))
			Expect(plain.runAsUsers).To(ConsistOf(HaveValue(Equal(int64(10000)))))
		},
		Entry("for nginx-demo", (*lhframework.Framework).NewNginxDeployment),
		Entry("for netshoot", (*lhframework.Framework).NewNetShootDeployment),
	)

	It("should fail without creating the Deployment when API discovery fails", func() {
		oldTimeout := framework.TestContext.OperationTimeout
		DeferCleanup(func() { framework.TestContext.OperationTimeout = oldTimeout })

		framework.TestContext.OperationTimeout = 1
		plain.failDiscovery = true

		Expect(InterceptGomegaFailure(func() { f.NewNginxDeployment(framework.ClusterA) })).To(
			MatchError(ContainSubstring("discover API groups for nettest UID selection")))
		Expect(plain.runAsUsers).To(BeEmpty())
	})
})

// fakeCluster serves API discovery and records the pod UIDs of created Deployments. It reports a Running pod so
// the builders' wait completes.
type fakeCluster struct {
	runAsUsers    []*int64
	failDiscovery bool
	openShift     bool
}

func (c *fakeCluster) start() *kubernetes.Clientset {
	server := httptest.NewServer(http.HandlerFunc(c.serve))
	DeferCleanup(server.Close)

	client, err := kubernetes.NewForConfig(&rest.Config{
		Host:          server.URL,
		ContentConfig: rest.ContentConfig{ContentType: "application/json"},
	})
	Expect(err).NotTo(HaveOccurred())

	return client
}

func (c *fakeCluster) serve(w http.ResponseWriter, r *http.Request) {
	defer GinkgoRecover()

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.URL.Path == "/apis" && c.failDiscovery:
		w.WriteHeader(http.StatusForbidden)
	case r.URL.Path == "/apis":
		groups := &metav1.APIGroupList{}
		if c.openShift {
			groups.Groups = []metav1.APIGroup{{Name: "security.openshift.io"}}
		}

		Expect(json.NewEncoder(w).Encode(groups)).To(Succeed())
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deployments"):
		deployment := &appsv1.Deployment{}
		Expect(json.NewDecoder(r.Body).Decode(deployment)).To(Succeed())

		c.runAsUsers = append(c.runAsUsers, deployment.Spec.Template.Spec.SecurityContext.RunAsUser)

		Expect(json.NewEncoder(w).Encode(deployment)).To(Succeed())
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pods"):
		Expect(json.NewEncoder(w).Encode(&corev1.PodList{
			Items: []corev1.Pod{{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
		})).To(Succeed())
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
