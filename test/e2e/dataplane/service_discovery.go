package dataplane

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/test/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := framework.NewDefaultFramework("dataplane-sd")

	When("a pod tries to resolve a service in a remote cluster", func() {
		It("should be able to discover the remote service sucessfully", func() {
			RunServiceDiscoveryTest(f)
		})
	})

	When("a pod tries to resolve a service which is present in local and remote cluster.", func() {
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
	nginxServiceClusterCIP := nginxServiceClusterC.Spec.ClusterIP
	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterB)
	cmd := []string{"curl", "nginx-demo"}
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

	kubeDnsService, _ := f.ClusterClients[framework.ClusterB].CoreV1().Services("kube-system").Get("kube-dns", metav1.GetOptions{})
	kubeDnsServiceIP := kubeDnsService.Spec.ClusterIP

	cmd = []string{"dig", "@" + kubeDnsServiceIP, "nginx-demo." + f.Namespace + ".svc.cluster2.local", "+short"}
	stdout, _, err = f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       netshootPodList.Items[0].Name,
		ContainerName: netshootPodList.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)

	Expect(err).NotTo(HaveOccurred())
	Expect(stdout).To(ContainSubstring(nginxServiceClusterCIP))
	f.DeleteService(framework.ClusterC, "nginx-demo")
	f.AwaitForMcsCrdDelete(framework.ClusterB, "nginx-demo")
	stdout, _, err = f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       netshootPodList.Items[0].Name,
		ContainerName: netshootPodList.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)

	Expect(err).NotTo(HaveOccurred())
	Expect(stdout).NotTo(ContainSubstring(nginxServiceClusterCIP))
}

func RunServiceDiscoveryLocalTest(f *framework.Framework) {
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
	clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)
	nginxServiceClusterBIP := nginxServiceClusterB.Spec.ClusterIP

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	_ = f.NewNginxService(framework.ClusterC)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterB)
	kubeDnsService, _ := f.ClusterClients[framework.ClusterB].CoreV1().Services("kube-system").Get("kube-dns", metav1.GetOptions{})
	kubeDnsServiceIP := kubeDnsService.Spec.ClusterIP

	cmd := []string{"dig", "@" + kubeDnsServiceIP, "nginx-demo." + f.Namespace + ".svc.cluster2.local", "+short"}
	stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       netshootPodList.Items[0].Name,
		ContainerName: netshootPodList.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)

	Expect(err).NotTo(HaveOccurred())
	Expect(stdout).To(ContainSubstring(nginxServiceClusterBIP))
}
