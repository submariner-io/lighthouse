package dataplane

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[dataplane] Test Service Discovery Across Clusters", func() {
	f := framework.NewFramework("dataplane-sd")

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

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true, true)
	verifyCurl(f, framework.ClusterB, netshootPodList.Items[0], nginxServiceClusterC.Name, true)

	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	verifyCurl(f, framework.ClusterB, netshootPodList.Items[0], nginxServiceClusterC.Name, false)
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

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterB, netshootPodList, true, false)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, true, true)
	verifyCurl(f, framework.ClusterB, netshootPodList.Items[0], nginxServiceClusterC.Name, true)

	f.DeleteService(framework.ClusterC, nginxServiceClusterC.Name)

	verifyClusterIpWithDig(f, framework.ClusterB, nginxServiceClusterC, netshootPodList, false, false)
}

func verifyClusterIpWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *v1.PodList, shouldContain bool, logOnly bool) {
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
		By(fmt.Sprintf("Validating dig result: %q", result))
		if doesContain && !shouldContain {
			return false || logOnly, fmt.Sprintf("expected execution result %q not to contain %q", result, serviceIP), nil
		}

		if !doesContain && shouldContain {
			return false || logOnly, fmt.Sprintf("expected execution result %q to contain %q", result, serviceIP), nil
		}

		return true, "", nil
	})
}

func verifyCurl(f *framework.Framework, cluster framework.ClusterIndex, pod v1.Pod, target string, expectingSuccess bool) {
	cmd := []string{"curl", target}
	stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       pod.Name,
		ContainerName: pod.Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, framework.ClusterB)
	if expectingSuccess {
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))
	} else {
		Expect(err).To(HaveOccurred())
	}
}
