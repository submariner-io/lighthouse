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
	"context"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/lighthouse/test/e2e/labels"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Dual-stack Service Discovery Across Clusters", Label(labels.ServiceDiscovery), func() {
	f := lhframework.NewFramework("discovery")

	BeforeEach(func(ctx context.Context) {
		if lhframework.IsClusterSetIPEnabled(ctx) {
			Skip("The clusterset IP feature is enabled globally - skipping the test")
		}

		if f.DetermineIPFamilyType(ctx, framework.ClusterB) != framework.DualStack {
			Skip("Dual-stack is not supported - skipping the test")
		}
	})

	When("a pod tries to resolve a dual-stack ClusterIP service in a remote cluster", func() {
		It("should be able to discover the remote service via either IPv4 or IPv6", func(ctx context.Context) {
			RunDualStackClusterIPDiscoveryTest(ctx, f)
		})
	})

	When("a pod tries to resolve a dual-stack headless service in a remote cluster", func() {
		It("should resolve the backing IPv4 and IPv6 pod IPs from the remote cluster", func(ctx context.Context) {
			RunDualStackHeadlessDiscoveryTest(ctx, f)
		})
	})
})

func RunDualStackClusterIPDiscoveryTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a dual-stack Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxServiceWithIPFamilyPolicy(ctx, framework.ClusterB, new(corev1.IPFamilyPolicyRequireDualStack))

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	epsList := f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	Expect(slices.IndexFunc(epsList.Items, func(eps discovery.EndpointSlice) bool {
		return eps.AddressType == discovery.AddressTypeIPv4
	})).To(BeNumerically(">=", 0), "IPv4 EndpointSlice not found")

	Expect(slices.IndexFunc(epsList.Items, func(eps discovery.EndpointSlice) bool {
		return eps.AddressType == discovery.AddressTypeIPv6
	})).To(BeNumerically(">=", 0), "IPv6 EndpointSlice not found")

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", f.GetServiceIP(ctx, framework.ClusterB, nginxServiceClusterB, corev1.IPv4Protocol), true)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", f.GetServiceIP(ctx, framework.ClusterB, nginxServiceClusterB, corev1.IPv6Protocol), true)
}

func RunDualStackHeadlessDiscoveryTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	nginxHeadlessClusterB := f.NewHeadlessServiceWithParams(ctx, "nginx-headless", "http", corev1.ProtocolTCP,
		map[string]string{"app": "nginx-demo"}, framework.ClusterB, new(corev1.IPFamilyPolicyRequireDualStack))

	f.NewServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	framework.By("Verifying IPv4")

	ipList, hostNameList := f.GetPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, false)

	f.VerifyIPsWithDigByFamily(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true, k8snet.IPv4)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, true, false, true)

	framework.By("Verifying IPv6")

	ipList, hostNameList = f.AwaitEndpointIPs(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 1,
		discovery.AddressTypeIPv6)

	f.VerifyIPsWithDigByFamily(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true, k8snet.IPv6)
	verifyHeadlessSRVRecordsWithDigByFamily(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList,
		checkedDomains, clusterBName, true, false, true, k8snet.IPv6)
}
