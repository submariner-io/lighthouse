package dataplane

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhFramework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := lhFramework.New("dataplane-sd").Framework

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

})

func RunServiceDiscoveryTest(f *framework.Framework) {
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
	clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	nginxServiceClusterC := f.NewNginxService(framework.ClusterC)
	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterB)
	cmd := []string{"curl", nginxServiceClusterC.Name}
	stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       netshootPodList.Items[0].Name,
		ContainerName: netshootPodList.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)

	Expect(err).NotTo(HaveOccurred())
	Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true)

	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, false)
}

func RunServiceDiscoveryLocalTest(f *framework.Framework) {
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
	clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	nginxServiceClusterC := f.NewNginxService(framework.ClusterC)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterB)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterB, netshootPodList, true)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true)

	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, false)
}

func verifyClusterIpWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *v1.PodList, shouldContain bool) {
	kubeDnsService, _ := framework.KubeClients[cluster].CoreV1().Services("kube-system").Get("kube-dns", metav1.GetOptions{})
	kubeDnsServiceIP := kubeDnsService.Spec.ClusterIP

	serviceIP := service.Spec.ClusterIP

	cmd := []string{"dig", "@" + kubeDnsServiceIP, service.Name + "." + f.Namespace + ".svc.cluster" + strconv.Itoa(int(cluster+1)) + ".local", "+short"}
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
		}, cluster)
		if err != nil {
			return nil, err
		}
		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		doesContain := strings.Contains(result.(string), serviceIP)
		if doesContain && !shouldContain {
			return false, fmt.Sprintf("expected execution result %q not to contain %q", result, serviceIP), nil
		}

		if !doesContain && shouldContain {
			return false, fmt.Sprintf("expected execution result %q to contain %q", result, serviceIP), nil
		}

		return true, "", nil
	})
}
