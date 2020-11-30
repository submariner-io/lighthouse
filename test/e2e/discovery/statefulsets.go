package discovery

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/discovery/v1beta1"
)

var _ = Describe("[discovery] Test Stateful Sets Discovery Across Clusters", func() {
	/*f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a podname from stateful set in a remote cluster", func() {
		It("should resolve the pod IP from the remote cluster", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunSSDiscoveryTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
		})
	})

	When("a pod tries to resolve a podname from stateful set in a local cluster", func() {
		It("should resolve the pod IP from the local cluster", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunSSDiscoveryLocalTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
		})
	})

	When("the number of active pods backing a stateful set changes", func() {
		It("should only resolve the IPs from the active pods", func() {
			if !framework.TestContext.GlobalnetEnabled {
				RunSSPodsAvailabilityTest(f)
			} else {
				framework.Skipf("Globalnet is enabled, skipping the test...")
			}
		})
	})

	*/
})

func RunSSDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Stateful Set on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName, appName, framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	verifyEndpointSlices(f.Framework, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB.Name, 1, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0, 0)

	verifyEndpointSlices(f.Framework, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB.Name, 1, false)
}

func RunSSDiscoveryLocalTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	// Create StatefulSet on ClusterB
	By(fmt.Sprintf("Creating an Nginx Stateful Set on on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName, appName, framework.ClusterB)

	// Create StatefulSet on ClusterA
	By(fmt.Sprintf("Creating an Nginx Stateful Set on on %q", clusterAName))

	nginxSSClusterA := f.NewNginxStatefulSet(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxServiceClusterA := f.NewNginxHeadlessServiceWithParams(nginxSSClusterA.Spec.ServiceName, appName, framework.ClusterA)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.NewServiceExport(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	verifyEndpointSlices(f.Framework, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB.Name, 2, true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1)

	verifyCount := 0

	for _, endpointSlice := range endpointSlices.Items {
		sourceCluster := endpointSlice.Labels[lhconstants.LabelSourceCluster]

		for _, endpoint := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f.Framework, framework.ClusterA, netshootPodList, endpoint, sourceCluster,
				nginxServiceClusterB.Name, checkedDomains, sourceCluster == clusterAName)
			verifyCount++
		}
	}

	Expect(verifyCount).To(Equal(2), "Mismatch in count of IPs to be validated with dig")

	f.DeleteServiceExport(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0, 0)
}

func RunSSPodsAvailabilityTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Stateful Set on on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	f.SetNginxStatefulSetReplicas(framework.ClusterB, 3)

	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName, appName, framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 3)

	verifyEndpointSlices(f.Framework, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB.Name, 3, true)

	f.SetNginxStatefulSetReplicas(framework.ClusterB, 1)

	for _, endpointSlice := range endpointSlices.Items {
		sourceCluster := endpointSlice.Labels[lhconstants.LabelSourceCluster]

		for _, endpoint := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f.Framework, framework.ClusterA, netshootPodList, endpoint, sourceCluster,
				nginxServiceClusterB.Name, checkedDomains, *endpoint.Hostname == "web-0")
		}
	}

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceImportCount(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0)
}

func verifyEndpointSlices(f *framework.Framework, targetCluster framework.ClusterIndex, netshootPodList *corev1.PodList,
	endpointSlices *v1beta1.EndpointSliceList, svcName string, verifyCount int, shouldContain bool) {
	count := 0

	for _, endpointSlice := range endpointSlices.Items {
		sourceCluster := endpointSlice.Labels[lhconstants.LabelSourceCluster]

		for _, endpoint := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f, targetCluster, netshootPodList, endpoint, sourceCluster,
				svcName, checkedDomains, shouldContain)
			count++
		}
	}

	Expect(count).To(Equal(verifyCount), "Mismatch in count of IPs to be validated with dig")
}

func verifyEndpointsWithDig(f *framework.Framework, targetCluster framework.ClusterIndex, targetPod *corev1.PodList,
	endpoint v1beta1.Endpoint, sourceCluster, service string, domains []string, shouldContain bool) {
	cmd := []string{"dig", "+short"}

	query := *endpoint.Hostname + "." + sourceCluster + "." + service

	for i := range domains {
		cmd = append(cmd, query+"."+f.Namespace+".svc."+domains[i])
	}

	op := "are"
	if !shouldContain {
		op += " not"
	}

	By(fmt.Sprintf("Executing %q to verify IPs %v for pod %q %q discoverable", strings.Join(cmd, " "), endpoint.Addresses, query, op))
	framework.AwaitUntil(" service IP verification", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
			Command:       cmd,
			Namespace:     f.Namespace,
			PodName:       targetPod.Items[0].Name,
			ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
			CaptureStdout: true,
			CaptureStderr: true,
		}, targetCluster)
		if err != nil {
			return nil, err
		}

		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		By(fmt.Sprintf("Validating that dig result %s %q", op, result))
		if len(endpoint.Addresses) == 0 && result != "" {
			return false, fmt.Sprintf("expected execution result %q to be empty", result), nil
		}
		for _, ip := range endpoint.Addresses {
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
