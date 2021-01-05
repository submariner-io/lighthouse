/*
© 2020 Red Hat, Inc. and others

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
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	clustersetDomain       = "clusterset.local"
)

// Both domains need to be checked, until the operator is updated to use clusterset
var checkedDomains = []string{clustersetDomain}

var _ = Describe("[discovery] Test Service Discovery Across Clusters", func() {

	f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a service in a remote cluster", func() {
		It("should be able to discover the remote service successfully", func() {
			RunServiceDiscoveryTest(f)
		})
	})

	When("a pod tries to resolve a service which is present locally and in a remote cluster", func() {
		It("should resolve the local service", func() {
			RunServiceDiscoveryLocalTest(f)
		})
	})

	When("service export is created before the service", func() {
		It("should resolve the service", func() {
			RunServiceExportTest(f)
		})
	})

	When("there are no active pods for a service", func() {
		It("should not resolve the service", func() {
			RunServicesPodAvailabilityTest(f)
		})
	})

	When("there are active pods for a service in only one cluster", func() {
		It("should not resolve the service on the cluster without active pods", func() {
			RunServicesPodAvailabilityMutliClusterTest(f)
		})
	})


	When("a pod tries to resolve a service in a specific remote cluster by its cluster name", func() {
		It("should resolve the service on the specified cluster", func() {
			RunServiceDiscoveryClusterNameTest(f)

	When("a pod tries to resolve a service multiple times", func() {
		It("should resolve the service from both the clusters in a round robin fashion", func() {
			RunServiceDiscoveryRoundRobinTest(f)
		})
	})
})

func RunServiceDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportDelete(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServiceDiscoveryLocalTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))
	// don't need ServiceExport for local service
	nginxServiceClusterA := f.Framework.NewNginxService(framework.ClusterA)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)
	clusterADomain := getClusterDomain(f.Framework, framework.ClusterA, netshootPodList)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList,
		[]string{clusterADomain}, "", true)

	f.DeleteService(framework.ClusterA, nginxServiceClusterA.Name)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportDelete(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServiceExportTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx ServiceExport on %q", clusterBName))
	f.NewServiceExport(framework.ClusterB, "nginx-demo", f.Namespace)
	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportDelete(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServicesPodAvailabilityTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	f.SetNginxReplicaSet(framework.ClusterB, 0)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
	f.SetNginxReplicaSet(framework.ClusterB, 2)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
}

func RunServicesPodAvailabilityMutliClusterTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	}

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))

	nginxServiceClusterA := f.NewNginxService(framework.ClusterA)

	f.AwaitGlobalnetIP(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	f.NewServiceExport(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	if svc, err := f.GetService(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace); err == nil {
		nginxServiceClusterA = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterA)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 2)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		"", true)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)

	f.SetNginxReplicaSet(framework.ClusterA, 0)

	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 1)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		"", false)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(framework.ClusterB, 0)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 0)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		"", false)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServiceDiscoveryClusterNameTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))

	nginxServiceClusterA := f.Framework.NewNginxService(framework.ClusterA)

	f.AwaitGlobalnetIP(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	f.NewServiceExport(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace); err == nil {
		nginxServiceClusterA = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterA)
		f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 2)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		clusterAName, true)

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterA, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)
	}

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		clusterBName, true)
}

func RunServiceDiscoveryRoundRobinTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.AwaitGlobalnetIP(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.Framework.NewNginxService(framework.ClusterC)

	f.AwaitGlobalnetIP(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	f.NewServiceExport(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	if svc, err := f.GetService(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace); err == nil {
		nginxServiceClusterC = svc
		f.AwaitServiceImportIP(framework.ClusterC, nginxServiceClusterC)
		f.AwaitEndpointSlices(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)
	}

	if svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace); err == nil {
		nginxServiceClusterB = svc
		f.AwaitServiceImportIP(framework.ClusterB, nginxServiceClusterB)
		f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)
	}

	var serviceIPList []string

	serviceIPClusterB, ok := nginxServiceClusterB.Annotations[submarinerIpamGlobalIp]
	if !ok {
		serviceIPClusterB = nginxServiceClusterB.Spec.ClusterIP
	}

	serviceIPList = append(serviceIPList, serviceIPClusterB)

	serviceIPClusterC, ok := nginxServiceClusterC.Annotations[submarinerIpamGlobalIp]
	if !ok {
		serviceIPClusterC = nginxServiceClusterC.Spec.ClusterIP
	}

	serviceIPList = append(serviceIPList, serviceIPClusterC)

	verifyRoundRobinWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB.Name, serviceIPList, netshootPodList, checkedDomains)
}

func verifyServiceIpWithDig(f *framework.Framework, srcCluster, targetCluster framework.ClusterIndex, service *corev1.Service,
	targetPod *corev1.PodList, domains []string, clusterName string, shouldContain bool) {
	var serviceIP string
	var ok bool

	serviceIP, ok = service.Annotations[submarinerIpamGlobalIp]
	if !ok || srcCluster == targetCluster {
		serviceIP = service.Spec.ClusterIP
	}

	cmd := []string{"dig", "+short"}

	var clusterDNSName string
	if clusterName != "" {
		clusterDNSName = clusterName + "."
	}

	for i := range domains {
		cmd = append(cmd, clusterDNSName+service.Name+"."+f.Namespace+".svc."+domains[i])
	}

	op := "is"
	if !shouldContain {
		op += " not"
	}

	By(fmt.Sprintf("Executing %q to verify IP %q for service %q %q discoverable", strings.Join(cmd, " "), serviceIP, service.Name, op))
	framework.AwaitUntil("verify if service IP is discoverable", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
			Command:       cmd,
			Namespace:     f.Namespace,
			PodName:       targetPod.Items[0].Name,
			ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
			CaptureStdout: true,
			CaptureStderr: true,
		}, srcCluster)
		if err != nil {
			return nil, err
		}

		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		doesContain := strings.Contains(result.(string), serviceIP)
		By(fmt.Sprintf("Validating that dig result %q %s %q", result, op, serviceIP))
		if doesContain && !shouldContain {
			return false, fmt.Sprintf("expected execution result %q not to contain %q", result, serviceIP), nil
		}

		if !doesContain && shouldContain {
			return false, fmt.Sprintf("expected execution result %q to contain %q", result, serviceIP), nil
		}

		return true, "", nil
	})
}

func verifyRoundRobinWithDig(f *framework.Framework, srcCluster framework.ClusterIndex, serviceName string, serviceIPList []string,
	targetPod *corev1.PodList, domains []string) {
	cmd := []string{"dig", "+short"}

	for i := range domains {
		cmd = append(cmd, serviceName+"."+f.Namespace+".svc."+domains[i])
	}

	op := "is"
	serviceIPMap := make(map[string]int)

	By(fmt.Sprintf("Executing %q to verify IPs %q for service %q %q discoverable", strings.Join(cmd, " "), serviceIPList, serviceName, op))

	for count := 0; count < 10; count++ {
		framework.AwaitUntil("verify if service IP is discoverable", func() (interface{}, error) {
			stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
				Command:       cmd,
				Namespace:     f.Namespace,
				PodName:       targetPod.Items[0].Name,
				ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
				CaptureStdout: true,
				CaptureStderr: true,
			}, srcCluster)
			if err != nil {
				return nil, err
			}

			return stdout, nil
		}, func(result interface{}) (bool, string, error) {
			var doesContain bool
			for _, serviceIp := range serviceIPList {
				doesContain = strings.Contains(result.(string), serviceIp)
				if doesContain {
					serviceIPMap[serviceIp]++
				}
			}
			By(fmt.Sprintf("Validating that dig result %s %q", op, result))
			return true, "", nil
		})
	}

	By(fmt.Sprintf("Validating that difference between the service IP %q which was returned %d times and  the service"+
		" IP %q which was returned %d times is within the threshold", serviceIPList[0], serviceIPMap[serviceIPList[0]],
		serviceIPList[1], serviceIPMap[serviceIPList[1]]))

	Expect(serviceIPMap[serviceIPList[0]]-serviceIPMap[serviceIPList[1]]%10 < 5).Should(BeTrue())
}

func getClusterDomain(f *framework.Framework, cluster framework.ClusterIndex, targetPod *corev1.PodList) string {
	/*
		Kubernetes adds --cluster-domain config to all pods' /etc/resolve.conf exactly as follows:
			search <namespace>.svc.cluster.local svc.cluster.local cluster.local <custom-domains>
	*/
	cmd := []string{"cat", "/etc/resolv.conf"}

	if stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       targetPod.Items[0].Name,
		ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, cluster); err == nil {
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, "search") {
				ss := strings.Split(line, " ")
				return ss[3]
			}
		}
	}
	// Backup option. Ideally we should never hit this.
	return "cluster" + strconv.Itoa(int(cluster+1)) + ".local"
}
