package discovery

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("[discovery] Test Headless Service Discovery Across Clusters", func() {

	f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a headless service in a remote cluster", func() {
		It("should resolve the backing pod IPs from the remote cluster", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunHeadlessDiscoveryTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
		})
	})

	When("a pod tries to resolve a headless service which is exported locally and in a remote cluster", func() {
		It("should resolve the backing pod IPs from both clusters", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunHeadlessDiscoveryLocalAndRemoteTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
		})
	})

	When("the number of active pods backing a service changes", func() {
		It("should only resolve the IPs from the active pods", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunHeadlessPodsAvailabilityTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
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

	ipList := f.GetEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, true)

	f.DeleteServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 0)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, false)
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

	ipListB := f.GetEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	ipListA := f.GetEndpointIPs(framework.ClusterA, nginxHeadlessClusterA.Name, nginxHeadlessClusterA.Namespace)
	ipList := append(ipListB, ipListA...)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, true)

	f.DeleteServiceExport(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 1)

	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListB, checkedDomains, false)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipListA, checkedDomains, true)
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

	ipList := f.AwaitEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 3)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, true)

	f.SetNginxReplicaSet(framework.ClusterB, 0)
	ipList = f.AwaitEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 0)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, false)

	f.SetNginxReplicaSet(framework.ClusterB, 2)
	ipList = f.AwaitEndpointIPs(framework.ClusterB, nginxHeadlessClusterB.Name, nginxHeadlessClusterB.Namespace, 2)
	verifyHeadlessIpsWithDig(f.Framework, framework.ClusterA, nginxHeadlessClusterB, netshootPodList, ipList, checkedDomains, true)
}

func verifyHeadlessIpsWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *corev1.PodList,
	ipList, domains []string, shouldContain bool) {
	cmd := []string{"dig", "+short"}
	for i := range domains {
		cmd = append(cmd, service.Name+"."+f.Namespace+".svc."+domains[i])
	}

	op := "are"
	if !shouldContain {
		op += " not"
	}

	By(fmt.Sprintf("Executing %q to verify IPs %v for service %q %q discoverable", strings.Join(cmd, " "), ipList, service.Name, op))
	framework.AwaitUntil(" service IP verification", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
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
