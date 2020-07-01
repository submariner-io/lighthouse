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
	v1 "k8s.io/api/core/v1"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	superclusterDomain     = "supercluster.local"
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

	When("service export is created before the service", func() {
		It("should resolve the service", func() {
			RunServiceExportTest(f)
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
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	se := f.AwaitServiceExportStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(se.Status.Conditions[0].Status).To(Equal(corev1.ConditionTrue))
	Expect(*se.Status.Conditions[0].Message).To(Equal("Service was successfully synced to the broker"))

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, false)
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
	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	se := f.AwaitServiceExportStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(se.Status.Conditions[0].Status).To(Equal(corev1.ConditionTrue))
	Expect(*se.Status.Conditions[0].Message).To(Equal("Service was successfully synced to the broker"))

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)
	clusterADomain := getClusterDomain(f.Framework, framework.ClusterA, netshootPodList)
	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterA, netshootPodList, clusterADomain, true)

	f.DeleteService(framework.ClusterA, nginxServiceClusterA.Name)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, false)
}

func RunServiceExportTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx ServiceExport on on %q", clusterBName))
	f.NewServiceExport(framework.ClusterB, "nginx-demo", f.Namespace)
	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))
	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))
	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	verifyServiceIpWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, superclusterDomain, false)
}

func verifyServiceIpWithDig(f *framework.Framework, cluster framework.ClusterIndex, service *corev1.Service, targetPod *v1.PodList, domain string, shouldContain bool) {
	var serviceIP string
	var ok bool

	if serviceIP, ok = service.Annotations[submarinerIpamGlobalIp]; !ok {
		serviceIP = service.Spec.ClusterIP
	}

	cmd := []string{"dig", service.Name + "." + f.Namespace + ".svc." + domain, "+short"}
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

func getClusterDomain(f *framework.Framework, cluster framework.ClusterIndex, targetPod *v1.PodList) string {
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
	//Backup option. Ideally we should never hit this.
	return "cluster" + strconv.Itoa(int(cluster+1)) + ".local"
}
