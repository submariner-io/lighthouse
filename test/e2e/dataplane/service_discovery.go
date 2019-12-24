package dataplane

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := framework.NewDefaultFramework("dataplane-sd")

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

	By(fmt.Sprintf("Executing dig to verify Nginx service is discoverable and service IP in cluster %q is returned", framework.ClusterC))
	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true)

	By(fmt.Sprintf("Deleting Nginx service %q", nginxServiceClusterC.Name))
	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	By(fmt.Sprintf("Executing dig to verify Nginx service is no longer discoverable"))
	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, false)
}

func RunServiceDiscoveryLocalTest(f *framework.Framework) {
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
	clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	nginxServiceClusterC := f.NewNginxService(framework.ClusterC)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterB)

	By(fmt.Sprintf("Executing dig to verify Nginx service is discoverable and service IP in cluster %q is returned", clusterBName))
	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterB, netshootPodList, true)

	By(fmt.Sprintf("Deleting Nginx service %q", nginxServiceClusterB.Name))
	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	By(fmt.Sprintf("Executing dig to verify Nginx service is discoverable and service IP in cluster %q is returned", clusterCName))
	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true)

	By(fmt.Sprintf("Deleting Nginx service %q", nginxServiceClusterC.Name))
	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	By(fmt.Sprintf("Executing dig to verify Nginx service is no longer discoverable"))
	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, false)
}

func verifyClusterIpWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *v1.PodList, shouldContain bool) {
	kubeDnsService, _ := f.ClusterClients[cluster].CoreV1().Services("kube-system").Get("kube-dns", metav1.GetOptions{})
	kubeDnsServiceIP := kubeDnsService.Spec.ClusterIP

	serviceIP := service.Spec.ClusterIP

	cmd := []string{"dig", "@" + kubeDnsServiceIP, service.Name + "." + f.Namespace + ".svc.cluster" + strconv.Itoa(int(cluster+1)) + ".local", "+short"}
	By(fmt.Sprintf("Executing %q to verify discovery of Nginx service IP %q", strings.Join(cmd, " "), serviceIP))
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
	}, func(result interface{}) (bool, error) {
		doesContain := strings.Contains(result.(string), serviceIP)
		if (doesContain && !shouldContain) || (!doesContain && shouldContain) {
			return false, nil
		}
		return true, nil
	})
}
