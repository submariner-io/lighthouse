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
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
)

var _ = Describe("Dual-stack Service Discovery Across Clusters", Label(TestLabel), func() {
	f := lhframework.NewFramework("discovery")

	BeforeEach(func() {
		if lhframework.IsClusterSetIPEnabled() {
			Skip("The clusterset IP feature is enabled globally - skipping the test")
		}

		if f.DetermineIPFamilyType(framework.ClusterB) != framework.DualStack {
			Skip("Dual-stack is not supported - skipping the test")
		}
	})

	When("a pod tries to resolve a dual-stack ClusterIP service in a remote cluster", func() {
		It("should be able to discover the remote service via either IPv4 or IPv6", func() {
			RunDualStackClusterIPDiscoveryTest(f)
		})
	})

	When("a pod tries to resolve a dual-stack headless service in a remote cluster", func() {
		It("should resolve the backing IPv4 and IPv6 pod IPs from the remote cluster", func() {
			RunDualStackHeadlessDiscoveryTest(f)
		})
	})
})

func RunDualStackClusterIPDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a dual-stack Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxServiceWithIPFamilyPolicy(framework.ClusterB, ptr.To(corev1.IPFamilyPolicyRequireDualStack))

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	epsList := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	Expect(slices.IndexFunc(epsList.Items, func(eps discovery.EndpointSlice) bool {
		return eps.AddressType == discovery.AddressTypeIPv4
	})).To(BeNumerically(">=", 0), "IPv4 EndpointSlice not found")

	Expect(slices.IndexFunc(epsList.Items, func(eps discovery.EndpointSlice) bool {
		return eps.AddressType == discovery.AddressTypeIPv6
	})).To(BeNumerically(">=", 0), "IPv6 EndpointSlice not found")

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", f.GetServiceIP(framework.ClusterB, nginxServiceClusterB, corev1.IPv4Protocol), true)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", f.GetServiceIP(framework.ClusterB, nginxServiceClusterB, corev1.IPv6Protocol), true)
}

func RunDualStackHeadlessDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	nginxHeadlessClusterB := f.NewHeadlessServiceWithParams("nginx-headless", "http", corev1.ProtocolTCP,
		map[string]string{"app": "nginx-demo"}, framework.ClusterB, ptr.To(corev1.IPFamilyPolicyRequireDualStack))

	f.NewServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	framework.By("Verifying IPv4")

	ipList, hostNameList := f.GetPodIPs(framework.ClusterB, nginxHeadlessClusterB, false)

	f.VerifyIPsWithDig(framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, true, false, true)

	framework.By("Verifying IPv6")

	ipList, hostNameList = f.AwaitEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 1,
		discovery.AddressTypeIPv6)

	f.VerifyIPsWithDigByFamily(framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true, k8snet.IPv6)
	verifyHeadlessSRVRecordsWithDigByFamily(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList,
		checkedDomains, clusterBName, true, false, true, k8snet.IPv6)
}
