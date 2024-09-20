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

package discovery

import (
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/constants"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Test Service Discovery Across Clusters", Label(ClusterSetIPTestLabel), func() {
	f := lhframework.NewFramework("discovery")

	When("clusterset IP is enabled for an exported service", func() {
		It("should resolve the allocated clusterset IP", func() {
			RunClusterSetIPTest(f)
		})
	})
})

func RunClusterSetIPTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.CreateServiceExport(framework.ClusterB, &mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nginxServiceClusterB.Name,
			Namespace:   nginxServiceClusterB.Namespace,
			Annotations: map[string]string{constants.UseClustersetIP: strconv.FormatBool(true)},
		},
	})

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	serviceImport := f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	Expect(serviceImport.Spec.IPs).To(HaveLen(1), "ServiceImport was not allocated an IP")

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "", serviceImport.Spec.IPs[0], true)
}
