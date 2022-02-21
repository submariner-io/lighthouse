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
	"strings"

	. "github.com/onsi/ginkgo"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
)

const (
	httpPortName = "http"
	opAre        = "are"
)

var _ = Describe("[discovery] Test Headless Service Discovery Across Clusters", func() {
	f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a headless service in a remote cluster", func() {
		It("should resolve the backing pod IPs from the remote cluster", func() {
			RunHeadlessDiscoveryTest(f)
		})
	})

	When("a pod tries to resolve a headless service which is exported locally and in a remote cluster", func() {
		It("should resolve the backing pod IPs from both clusters", func() {
			RunHeadlessDiscoveryLocalAndRemoteTest(f)
		})
	})

	When("the number of active pods backing a service changes", func() {
		It("should only resolve the IPs from the active pods", func() {
			RunHeadlessPodsAvailabilityTest(f)
		})
	})

	When("a pod tries to resolve a headless service in a specific remote cluster by its cluster name", func() {
		It("should resolve the backing pod IPs from the specified remote cluster", func() {
			RunHeadlessDiscoveryClusterNameTest(f)
		})
	})
})

func RunHeadlessDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	ipList, hostNameList := f.GetPodIPs(framework.ClusterB, nginxHeadlessClusterB)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		clusterBName, true)

	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, true, false, true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, true)

	f.DeleteServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 0)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", false)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameList, checkedDomains,
		clusterBName, false, false, false)
}

func RunHeadlessDiscoveryLocalAndRemoteTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)
	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxHeadlessClusterA := f.NewNginxHeadlessService(framework.ClusterA)

	f.NewServiceExport(framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	ipListB, hostNameListB := f.GetPodIPs(framework.ClusterB, nginxHeadlessClusterB)
	ipListA, hostNameListA := f.GetPodIPs(framework.ClusterA, nginxHeadlessClusterA)

	var ipList []string
	ipList = append(ipList, ipListB...)
	ipList = append(ipList, ipListA...)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)

	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB, checkedDomains,
		clusterBName, true, false, true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA, checkedDomains,
		clusterAName, true, false, true)
	f.DeleteServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 1)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListB, checkedDomains,
		"", false)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListA, checkedDomains,
		"", true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB, checkedDomains,
		clusterBName, true, false, false)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA, checkedDomains,
		clusterAName, true, false, true)
}

func RunHeadlessPodsAvailabilityTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	f.SetNginxReplicaSet(framework.ClusterB, 3)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	ipList, _ := f.AwaitPodIPs(framework.ClusterB, nginxHeadlessClusterB, 3)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(framework.ClusterB, 0)
	ipList, _ = f.AwaitPodIPs(framework.ClusterB, nginxHeadlessClusterB, 0)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", false)

	f.SetNginxReplicaSet(framework.ClusterB, 2)
	ipList, _ = f.AwaitPodIPs(framework.ClusterB, nginxHeadlessClusterB, 2)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains,
		"", true)
}

func RunHeadlessDiscoveryClusterNameTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxHeadlessClusterA := f.NewNginxHeadlessService(framework.ClusterA)

	f.NewServiceExport(framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxHeadlessClusterB := f.NewNginxHeadlessService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	ipListClusterA, hostNameListA := f.GetPodIPs(framework.ClusterA, nginxHeadlessClusterA)
	ipListClusterB, hostNameListB := f.GetPodIPs(framework.ClusterB, nginxHeadlessClusterB)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterA, netshootPodList, ipListClusterA, checkedDomains,
		clusterAName, true)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListClusterB, checkedDomains,
		clusterBName, true)

	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA, checkedDomains,
		clusterAName, true, true, true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB, checkedDomains,
		clusterBName, true, true, true)

	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListA, checkedDomains,
		clusterAName, false, true, true)
	verifyHeadlessSRVRecordsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, hostNameListB, checkedDomains,
		clusterBName, false, true, true)
}

// nolint:unparam // cluster` always receives `framework.ClusterA`.
func verifyHeadlessIpsWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *corev1.PodList,
	ipList, domains []string, clusterName string, shouldContain bool) {
	cmd := []string{"dig", "+short"}

	var clusterDNSName string
	if clusterName != "" {
		clusterDNSName = clusterName + "."
	}

	for i := range domains {
		cmd = append(cmd, clusterDNSName+service.Name+"."+f.Namespace+".svc."+domains[i])
	}

	op := opAre
	if !shouldContain {
		op += not
	}

	By(fmt.Sprintf("Executing %q to verify IPs %v for service %q %q discoverable", strings.Join(cmd, " "), ipList, service.Name, op))
	framework.AwaitUntil(" service IP verification", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(&framework.ExecOptions{
			Command:       cmd,
			Namespace:     f.Namespace,
			PodName:       targetPod.Items[0].Name,
			ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
			CaptureStdout: true,
			CaptureStderr: true,
		}, cluster)
		if err != nil {
			return nil, err
		}

		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		By(fmt.Sprintf("Validating that dig result %s %q", op, result))
		if len(ipList) == 0 && result != "" {
			return false, fmt.Sprintf("expected execution result %q to be empty", result), nil
		}
		for _, ip := range ipList {
			doesContain := strings.Contains(result.(string), ip)
			if doesContain && !shouldContain {
				return false, fmt.Sprintf("expected execution result %q not to contain %q", result, ip), nil
			}

			if !doesContain && shouldContain {
				return false, fmt.Sprintf("expected execution result %q to contain %q", result, ip), nil
			}
		}

		return true, "", nil
	})
}

// nolint:gocognit,unparam // This really isn't that complex and would be awkward to refactor.
func verifyHeadlessSRVRecordsWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service,
	targetPod *corev1.PodList, hostNameList, domains []string, clusterName string, withPort, withcluster, shouldContain bool) {
	ports := service.Spec.Ports
	for i := range domains {
		for j := range ports {
			port := &ports[j]
			cmd, domainName := createSRVQuery(f, port, service, domains[i], clusterName, withPort, withcluster)
			op := opAre

			if !shouldContain {
				op += not
			}

			By(fmt.Sprintf("Executing %q to verify hostNames %v for service %q %q discoverable",
				strings.Join(cmd, " "), hostNameList, service.Name, op))
			framework.AwaitUntil(" service IP verification", func() (interface{}, error) {
				stdout, _, err := f.ExecWithOptions(&framework.ExecOptions{
					Command:       cmd,
					Namespace:     f.Namespace,
					PodName:       targetPod.Items[0].Name,
					ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
					CaptureStdout: true,
					CaptureStderr: true,
				}, cluster)
				if err != nil {
					return nil, err
				}

				return stdout, nil
			}, func(result interface{}) (bool, string, error) {
				By(fmt.Sprintf("Validating that dig result %s %q", op, result))
				if len(hostNameList) == 0 && result != "" {
					return false, fmt.Sprintf("expected execution result %q to be empty", result), nil
				}
				for _, hostName := range hostNameList {
					hostDNS := hostName + "." + domainName
					doesContain := strings.Contains(result.(string), strconv.Itoa(int(port.Port))) &&
						strings.Contains(result.(string), hostDNS)
					if doesContain && !shouldContain {
						return false, fmt.Sprintf("expected execution result %q not to contain %q and %d", result, hostDNS, int(port.Port)), nil
					}

					if !doesContain && shouldContain {
						return false, fmt.Sprintf("expected execution result %q to contain %q and %d", result, hostDNS, int(port.Port)), nil
					}
				}

				return true, "", nil
			})
		}
	}
}

func createSRVQuery(f *framework.Framework, port *corev1.ServicePort, service *corev1.Service,
	domain string, clusterName string, withPort, withcluster bool) (cmd []string, domainName string) {
	cmd = []string{"dig", "+short", "SRV"}

	domainName = service.Name + "." + f.Namespace + ".svc." + domain
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
