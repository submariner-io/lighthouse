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
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestFramework(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Framework Suite")
}

var _ = Describe("Nettest helper Deployments", func() {
	var (
		openShift *fakeCluster
		plain     *fakeCluster
		f         *lhframework.Framework
	)

	BeforeEach(func() {
		openShift = newFakeCluster(metav1.APIGroup{Name: "security.openshift.io"})
		plain = newFakeCluster()

		oldClients := framework.KubeClients
		DeferCleanup(func() { framework.KubeClients = oldClients })

		framework.KubeClients = []*kubernetes.Clientset{plain.client(), openShift.client()}

		f = &lhframework.Framework{Framework: framework.NewBareFramework("nettest-uid")}
		f.Namespace = "nettest-uid"
	})

	DescribeTable("should set runAsUser only on non-OpenShift clusters",
		func(ctx SpecContext, build func(*lhframework.Framework, context.Context, framework.ClusterIndex) *corev1.PodList) {
			build(f, ctx, framework.ClusterB)
			build(f, ctx, framework.ClusterA)

			Expect(openShift.runAsUsers).To(ConsistOf(BeNil()))
			Expect(plain.runAsUsers).To(ConsistOf(HaveValue(Equal(int64(10000)))))
		},
		Entry("for nginx-demo", (*lhframework.Framework).NewNginxDeployment),
		Entry("for netshoot", (*lhframework.Framework).NewNetShootDeployment),
	)

	When("API discovery fails", func() {
		BeforeEach(func() {
			oldTimeout := framework.TestContext.OperationTimeout
			DeferCleanup(func() { framework.TestContext.OperationTimeout = oldTimeout })

			framework.TestContext.OperationTimeout = 1
			plain.RouteToHandler(http.MethodGet, "/apis", ghttp.RespondWith(http.StatusForbidden, nil))
		})

		It("should fail without creating the Deployment", func(ctx SpecContext) {
			Expect(InterceptGomegaFailure(func() { f.NewNginxDeployment(ctx, framework.ClusterA) })).To(
				MatchError(ContainSubstring("discover API groups")))
			Expect(plain.runAsUsers).To(BeEmpty())
		})
	})
})

// fakeCluster serves the API groups it is given and records the pod UIDs of created Deployments. It reports a
// Running pod so the builders' wait completes.
type fakeCluster struct {
	*ghttp.Server
	runAsUsers []*int64
}

func newFakeCluster(groups ...metav1.APIGroup) *fakeCluster {
	c := &fakeCluster{Server: ghttp.NewServer()}
	DeferCleanup(c.Close)

	c.RouteToHandler(http.MethodGet, "/api", ghttp.RespondWith(http.StatusNotFound, nil))
	c.RouteToHandler(http.MethodGet, "/apis", ghttp.RespondWithJSONEncoded(http.StatusOK, &metav1.APIGroupList{Groups: groups}))
	c.RouteToHandler(http.MethodPost, regexp.MustCompile("/deployments$"), func(w http.ResponseWriter, r *http.Request) {
		deployment := &appsv1.Deployment{}
		Expect(json.NewDecoder(r.Body).Decode(deployment)).To(Succeed())

		c.runAsUsers = append(c.runAsUsers, deployment.Spec.Template.Spec.SecurityContext.RunAsUser)

		ghttp.RespondWithJSONEncoded(http.StatusCreated, deployment)(w, r)
	})
	c.RouteToHandler(http.MethodGet, regexp.MustCompile("/pods$"), ghttp.RespondWithJSONEncoded(http.StatusOK, &corev1.PodList{
		Items: []corev1.Pod{{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
	}))

	return c
}

func (c *fakeCluster) client() *kubernetes.Clientset {
	return kubernetes.NewForConfigOrDie(&rest.Config{Host: c.URL(), ContentConfig: rest.ContentConfig{ContentType: "application/json"}})
}
