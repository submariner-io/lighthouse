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

package internal

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[internal] Test Service Discovery Across Clusters", func() {
	f := lhframework.NewFramework("internal")

	When("the namespace for an imported remote service does not exist locally", func() {
		It("should not resolve the remote service in the local cluster", func() {
			RunNonexistentNamespaceDiscoveryTest(f)
		})
	})
})

func RunNonexistentNamespaceDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	netshootNS, err := framework.KubeClients[framework.ClusterA].CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("e2e-tests-%v-", f.BaseName),
		},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "Error creating namespace")

	By(fmt.Sprintf("Created namespace %q for netshoot deployment on %q", netshootNS.Name, clusterAName))

	f.AddNamespacesToDelete(netshootNS)

	Expect(framework.KubeClients[framework.ClusterA].CoreV1().Namespaces().Delete(context.TODO(),
		f.Namespace, metav1.DeleteOptions{})).To(Succeed())

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeploymentInNS(framework.ClusterA, netshootNS.Name)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc

	for i := 1; i <= 5; i++ {
		time.Sleep(500 * time.Millisecond)
		f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, lhframework.CheckedDomains,
			"", false)
	}

	By(fmt.Sprintf("Recreating namespace %q on %q", f.Namespace, clusterAName))

	_, err = framework.KubeClients[framework.ClusterA].CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: f.Namespace,
		},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "Error creating namespace")

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, lhframework.CheckedDomains,
		"", true)
}
