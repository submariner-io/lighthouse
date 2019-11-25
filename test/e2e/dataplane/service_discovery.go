package dataplane

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/test/e2e/framework"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := framework.NewDefaultFramework("dataplane-sd")

	When("a pod tries to resolve a service in remote cluster", func() {
		BeforeEach(func() {
		})

		It("Should be able to ping successfully", func() {
			RunServiceDiscoveryTest(f)
		})
	})
})

func RunServiceDiscoveryTest(f *framework.Framework) {
	By("Creating a Nginx Deployment on cluster3")
	nginxDeployment := f.NewDeployment(&framework.DeploymentConfig{
		Type:         framework.NginxDeployment,
		Cluster:      framework.ClusterC,
		ReplicaCount: 1,
		PodName:      "nginx-demo",
	})
	nginxDeployment.AwaitSuccessfulFinish()
	By("Creating a Nginx Service on cluster3")
	service := f.NewService(&framework.ServiceConfig{
		Type:    framework.NginxService,
		Cluster: framework.ClusterC,
	})
	By("Creating a Netshoot Deployment on cluster2")
	netShootDeployment := f.NewDeployment(&framework.DeploymentConfig{
		Type:         framework.NetShootDeployment,
		Cluster:      framework.ClusterB,
		ServiceIP:    service.Service.Spec.ClusterIP,
		ReplicaCount: 1,
		PodName:      "netshoot",
	})
	cmd := []string{"curl", "nginx-demo"}
	netShootDeployment.AwaitSuccessfulFinish()
	stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       netShootDeployment.PodList.Items[0].Name,
		ContainerName: netShootDeployment.PodList.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)
	Expect(err).NotTo(HaveOccurred())
	Expect(stdout).To(ContainSubstring("Welcome to nginx!"))
}
