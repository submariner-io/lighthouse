package dataplane

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/test/e2e/framework"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := framework.NewDefaultFramework("dataplane-sd")

	When("a pod tries to resolve a service in a remote cluster", func() {
		It("should be able to ping it successfully", func() {
			RunServiceDiscoveryTest(f)
		})
	})
})

func RunServiceDiscoveryTest(f *framework.Framework) {
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
	clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewDeployment(&framework.DeploymentConfig{
		Type:         framework.NginxDeployment,
		Cluster:      framework.ClusterC,
		ReplicaCount: 1,
		PodName:      "nginx-demo",
	})

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))
	service := f.NewService(&framework.ServiceConfig{
		Type:    framework.NginxService,
		Cluster: framework.ClusterC,
	})

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterBName))
	netShootDeployment := f.NewDeployment(&framework.DeploymentConfig{
		Type:         framework.NetShootDeployment,
		Cluster:      framework.ClusterB,
		ServiceIP:    service.Service.Spec.ClusterIP,
		ReplicaCount: 1,
		PodName:      "netshoot",
	})
	cmd := []string{"curl", "nginx-demo"}
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
