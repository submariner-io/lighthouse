package discovery

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
)

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

})

func RunServiceDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.KubeContexts[framework.ClusterA]
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	verifyServiceIpWithDig(f, framework.ClusterA, nginxServiceClusterB, netshootPodList, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f, framework.ClusterA, nginxServiceClusterB, netshootPodList, false)
}

func RunServiceDiscoveryLocalTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.KubeContexts[framework.ClusterA]
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))
	// don't need ServiceExport for local service
	nginxServiceClusterA := f.Framework.NewNginxService(framework.ClusterA)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	verifyServiceIpWithDig(f, framework.ClusterA, nginxServiceClusterA, netshootPodList, true)

	f.DeleteService(framework.ClusterA, nginxServiceClusterA.Name)

	verifyServiceIpWithDig(f, framework.ClusterA, nginxServiceClusterB, netshootPodList, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f, framework.ClusterA, nginxServiceClusterB, netshootPodList, false)
}

func verifyServiceIpWithDig(lhframework *lhframework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *v1.PodList, shouldContain bool) {
	var serviceIP string
	var ok bool

	if serviceIP, ok = service.Annotations[submarinerIpamGlobalIp]; !ok {
		serviceIP = service.Spec.ClusterIP
	}
	var cmd []string
	lighthouseDnsDeployment := lhframework.GetDeployment(cluster, "submariner-lighthouse-coredns", "submariner-operator")
	if lighthouseDnsDeployment == nil {
		cmd = []string{"dig", service.Name + "." + lhframework.Framework.Namespace + ".svc.cluster" + strconv.Itoa(int(cluster+1)) + ".local", "+short"}
	} else {
		cmd = []string{"dig", service.Name + "." + lhframework.Framework.Namespace + ".svc.supercluster.local", "+short"}
	}
	op := "is"
	if !shouldContain {
		op += " not"
	}
	By(fmt.Sprintf("Executing %q to verify IP %q for service %q %q discoverable", strings.Join(cmd, " "), serviceIP, service.Name, op))
	framework.AwaitUntil("verify if service IP is discoverable", func() (interface{}, error) {
		stdout, _, err := lhframework.Framework.ExecWithOptions(framework.ExecOptions{
			Command:       cmd,
			Namespace:     lhframework.Framework.Namespace,
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
		By(fmt.Sprintf("Validating that dig result %s %q", op, result))
		if doesContain && !shouldContain {
			return false, fmt.Sprintf("expected execution result %q not to contain %q", result, serviceIP), nil
		}

		if !doesContain && shouldContain {
			return false, fmt.Sprintf("expected execution result %q to contain %q", result, serviceIP), nil
		}

		return true, "", nil
	})
}
