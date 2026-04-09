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
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/lighthouse/test/e2e/labels"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Test Headless Service Discovery Across Clusters", Label(labels.ServiceDiscovery), func() {
	f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a headless service in a remote cluster", func() {
		It("should resolve the backing pod IPs from the remote cluster", func(ctx context.Context) {
			RunHeadlessDiscoveryTest(ctx, f)
		})
	})

	When("a pod tries to resolve a headless service which is exported locally and in a remote cluster", func() {
		It("should resolve the backing pod IPs from both clusters", func(ctx context.Context) {
			RunHeadlessDiscoveryLocalAndRemoteTest(ctx, f)
		})
	})

	When("a pod tries to resolve a headless service without selector in a remote cluster", func() {
		It("should resolve the backing endpoint IPs from the remote cluster", func(ctx context.Context) {
			RunHeadlessEndpointDiscoveryTest(ctx, f)
		})
	})

	When("the number of active pods backing a service changes", func() {
		It("should only resolve the IPs from the active pods", func(ctx context.Context) {
			RunHeadlessPodsAvailabilityTest(ctx, f)
		})

		It("should resolve the local pod IPs", func(ctx context.Context) {
			RunHeadlessPodsAvailabilityTestLocal(ctx, f)
		})
	})

	When("a pod tries to resolve a headless service in a specific remote cluster by its cluster name", func() {
		It("should resolve the backing pod IPs from the specified remote cluster", func(ctx context.Context) {
			RunHeadlessDiscoveryClusterNameTest(ctx, f)
		})
	})
})

func RunHeadlessDiscoveryTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipList, hostNameList := f.GetPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, false)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		clusterBName, true)

	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, true, false, true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, true)

	f.DeleteServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxHeadlessClusterB, 0)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", false)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, false)
}

func RunHeadlessDiscoveryLocalAndRemoteTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)
	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxHeadlessClusterB, 1)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(ctx, framework.ClusterA)
	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxHeadlessClusterA := f.NewNginxHeadlessService(ctx, framework.ClusterA)

	f.NewServiceExport(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxHeadlessClusterA, 2)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipListB, hostNameListB := f.GetPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, false)
	ipListA, hostNameListA := f.GetPodIPs(ctx, framework.ClusterA, nginxHeadlessClusterA, true)

	ipList := make([]string, 0, len(ipListB)+len(ipListA))
	ipList = append(ipList, ipListB...)
	ipList = append(ipList, ipListA...)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)

	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB,
		checkedDomains, clusterBName, true, false, true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA,
		checkedDomains, clusterAName, true, false, true)
	f.DeleteServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxHeadlessClusterB, 1)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListB, checkedDomains,
		"", false)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListA, checkedDomains,
		"", true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB,
		checkedDomains, clusterBName, true, false, false)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA,
		checkedDomains, clusterAName, true, false, true)
}

func RunHeadlessEndpointDiscoveryTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating a Headless Service without selector on %q", clusterBName))

	headlessClusterB := f.NewHeadlessServiceEndpointIP(ctx, framework.ClusterB)

	framework.By("Creating an endpoint")
	f.NewEndpointForHeadlessService(ctx, framework.ClusterB, headlessClusterB)

	framework.By("Exporting the service %q")
	f.NewServiceExport(ctx, framework.ClusterB, headlessClusterB.Name, headlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, headlessClusterB.Name, headlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipList, hostNameList := f.GetEndpointIPs(ctx, framework.ClusterB, headlessClusterB)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, headlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, headlessClusterB, netshootPodList, ipList, checkedDomains,
		clusterBName, true)

	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, headlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, headlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, true, false, true)

	f.DeleteServiceExport(ctx, framework.ClusterB, headlessClusterB.Name, headlessClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, headlessClusterB, 0)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, headlessClusterB, netshootPodList, ipList, checkedDomains,
		"", false)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, headlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, false)
}

func RunHeadlessPodsAvailabilityTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)
	f.SetNginxReplicaSet(ctx, framework.ClusterB, 3)

	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipList, _ := f.AwaitPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, 3, false)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(ctx, framework.ClusterB, 0)
	ipList, _ = f.AwaitPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, 0, false)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", false)

	f.SetNginxReplicaSet(ctx, framework.ClusterB, 2)
	ipList, _ = f.AwaitPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, 2, false)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
}

func RunHeadlessPodsAvailabilityTestLocal(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(ctx, framework.ClusterA)
	f.SetNginxReplicaSet(ctx, framework.ClusterA, 3)

	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxHeadlessClusterA := f.NewNginxHeadlessService(ctx, framework.ClusterA)

	f.NewServiceExport(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipList, _ := f.AwaitPodIPs(ctx, framework.ClusterA, nginxHeadlessClusterA, 3, true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterA, netshootPodList, ipList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(ctx, framework.ClusterA, 0)
	ipList, _ = f.AwaitPodIPs(ctx, framework.ClusterA, nginxHeadlessClusterA, 0, true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterA, netshootPodList, ipList, checkedDomains,
		"", false)

	f.SetNginxReplicaSet(ctx, framework.ClusterA, 2)
	ipList, _ = f.AwaitPodIPs(ctx, framework.ClusterA, nginxHeadlessClusterA, 2, true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterA, netshootPodList, ipList, checkedDomains,
		"", true)
}

func RunHeadlessDiscoveryClusterNameTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(ctx, framework.ClusterA)

	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxHeadlessClusterA := f.NewNginxHeadlessService(ctx, framework.ClusterA)

	f.NewServiceExport(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	ipListClusterA, hostNameListA := f.GetPodIPs(ctx, framework.ClusterA, nginxHeadlessClusterA, true)
	ipListClusterB, hostNameListB := f.GetPodIPs(ctx, framework.ClusterB, nginxHeadlessClusterB, false)

	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterA, netshootPodList, ipListClusterA, checkedDomains,
		clusterAName, true)
	f.VerifyIPsWithDig(ctx, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListClusterB, checkedDomains,
		clusterBName, true)

	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA,
		checkedDomains, clusterAName, true, true, true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB,
		checkedDomains, clusterBName, true, true, true)

	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA,
		checkedDomains, clusterAName, false, true, true)
	verifyHeadlessSRVRecordsWithDig(ctx, f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB,
		checkedDomains, clusterBName, false, true, true)
}

func verifyHeadlessSRVRecordsWithDig(ctx context.Context, f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service,
	targetPod *corev1.PodList, hostNameList, domains []string, clusterName string, withPort, withcluster, shouldContain bool,
) {
	verifyHeadlessSRVRecordsWithDigByFamily(ctx, f, cluster, service, targetPod, hostNameList, domains, clusterName, withPort, withcluster,
		shouldContain, k8snet.IPv4)
}

//nolint:gocognit // This really isn't that complex and would be awkward to refactor.
func verifyHeadlessSRVRecordsWithDigByFamily(ctx context.Context, f *framework.Framework, cluster framework.ClusterIndex,
	service *corev1.Service, targetPod *corev1.PodList, hostNameList, domains []string, clusterName string, withPort, withcluster,
	shouldContain bool, ipFamily k8snet.IPFamily,
) {
	ports := service.Spec.Ports
	for i := range domains {
		for j := range ports {
			port := &ports[j]
			cmd, domainName := createSRVQuery(f, port, service, domains[i], clusterName, withPort, withcluster, ipFamily)
			op := "are"

			if !shouldContain {
				op += " not"
			}

			framework.By(fmt.Sprintf("Executing %q to verify hostNames %v for service %q %q discoverable",
				strings.Join(cmd, " "), hostNameList, service.Name, op))
			framework.AwaitUntil(ctx, " service IP verification", func(ctx context.Context) (string, error) {
				stdout, _, err := f.ExecWithOptions(ctx, &framework.ExecOptions{
					Command:       cmd,
					Namespace:     f.Namespace,
					PodName:       targetPod.Items[0].Name,
					ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
					CaptureStdout: true,
					CaptureStderr: true,
				}, cluster)
				if err != nil {
					return "", err
				}

				return stdout, nil
			}, func(result string) (bool, string, error) {
				framework.By(fmt.Sprintf("Validating that dig result %s %q", op, result))

				if len(hostNameList) == 0 && result != "" {
					return false, fmt.Sprintf("expected execution result %q to be empty", result), nil
				}

				for _, hostName := range hostNameList {
					hostDNS := hostName + "." + domainName
					doesContain := strings.Contains(result, strconv.Itoa(int(port.Port))) &&
						strings.Contains(result, hostDNS)

					if doesContain && !shouldContain {
						framework.Logf("expected execution result %q not to contain %q and %d", result, hostDNS, int(port.Port))
						return false, fmt.Sprintf("expected execution result %q not to contain %q and %d", result, hostDNS, int(port.Port)), nil
					}

					if !doesContain && shouldContain {
						framework.Logf("expected execution result %q to contain %q and %d", result, hostDNS, int(port.Port))
						return false, fmt.Sprintf("expected execution result %q to contain %q and %d", result, hostDNS, int(port.Port)), nil
					}
				}

				return true, "", nil
			})
		}
	}
}

func createSRVQuery(f *framework.Framework, port *corev1.ServicePort, service *corev1.Service,
	domain string, clusterName string, withPort, withcluster bool, ipFamily k8snet.IPFamily,
) ([]string, string) {
	cmd := []string{"dig", "+short"}

	if ipFamily == k8snet.IPv6 {
		cmd = append(cmd, "AAAA")
	}

	cmd = append(cmd, "SRV")

	domainName := lhframework.BuildServiceDNSName("", service.Name, f.Namespace, domain)
	clusterDNSName := domainName

	if withcluster {
		clusterDNSName = clusterName + "." + clusterDNSName
	}

	portDNS := clusterDNSName

	if withPort {
		portDNS = strings.ToLower(port.Name+"."+string(port.Protocol)+".") + portDNS
	}

	cmd = append(cmd, portDNS)

	return cmd, fmt.Sprintf("%s.%s", clusterName, domainName)
}
