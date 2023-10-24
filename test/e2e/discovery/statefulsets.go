/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package discovery

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/constants"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
)

const httpPortName = "http"

var _ = Describe("Test Stateful Sets Discovery Across Clusters", Label(TestLabel), func() {
	f := lhframework.NewFramework("discovery")

	When("a pod tries to resolve a podname from stateful set in a remote cluster", func() {
		It("should resolve the pod IP from the remote cluster", func() {
			RunSSDiscoveryTest(f)
		})
	})

	When("a pod tries to resolve a podname from stateful set in a local cluster", func() {
		It("should resolve the pod IP from the local cluster", func() {
			RunSSDiscoveryLocalTest(f)
		})
	})

	When("the number of active pods backing a stateful set changes", func() {
		It("should only resolve the IPs from the active pods", func() {
			RunSSPodsAvailabilityTest(f)
		})
	})
})

func RunSSDiscoveryTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Stateful Set on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName,
		httpPortName, corev1.ProtocolTCP, map[string]string{"app": appName}, framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	verifyEndpointSlices(f, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB, 1, true, clusterAName)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0, 0)

	verifyEndpointSlices(f, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB, 1, false, clusterAName)
}

func RunSSDiscoveryLocalTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	// Create StatefulSet on ClusterB
	By(fmt.Sprintf("Creating an Nginx Stateful Set on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName,
		httpPortName, corev1.ProtocolTCP, map[string]string{"app": appName}, framework.ClusterB)

	// Create StatefulSet on ClusterA
	By(fmt.Sprintf("Creating an Nginx Stateful Set on %q", clusterAName))

	nginxSSClusterA := f.NewNginxStatefulSet(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterAName))

	nginxServiceClusterA := f.NewHeadlessServiceWithParams(nginxSSClusterA.Spec.ServiceName,
		httpPortName, corev1.ProtocolTCP, map[string]string{"app": appName}, framework.ClusterA)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.NewServiceExport(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	verifyEndpointSlices(f, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB, 2, true, clusterAName)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)

	verifyCount := 0

	for i := range endpointSlices.Items {
		endpointSlice := &endpointSlices.Items[i]
		sourceCluster := endpointSlice.Labels[constants.MCSLabelSourceCluster]

		for j := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f, framework.ClusterA, netshootPodList, &endpointSlice.Endpoints[j], sourceCluster,
				nginxServiceClusterB, checkedDomains, sourceCluster == clusterAName, sourceCluster == clusterAName)
			verifyCount++
		}
	}

	Expect(verifyCount).To(Equal(2), "Mismatch in count of IPs to be validated with dig")

	f.DeleteServiceExport(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 0, 0)
}

func RunSSPodsAvailabilityTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Stateful Set on %q", clusterBName))

	nginxSSClusterB := f.NewNginxStatefulSet(framework.ClusterB)
	f.SetNginxStatefulSetReplicas(framework.ClusterB, 3)

	appName := nginxSSClusterB.Spec.Selector.MatchLabels["app"]

	By(fmt.Sprintf("Creating a Nginx Headless Service on %q", clusterBName))

	nginxServiceClusterB := f.NewHeadlessServiceWithParams(nginxSSClusterB.Spec.ServiceName,
		httpPortName, corev1.ProtocolTCP, map[string]string{"app": appName}, framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	endpointSlices := f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 3)

	verifyEndpointSlices(f, framework.ClusterA, netshootPodList, endpointSlices, nginxServiceClusterB, 3, true, clusterAName)

	f.SetNginxStatefulSetReplicas(framework.ClusterB, 1)

	for i := range endpointSlices.Items {
		endpointSlice := &endpointSlices.Items[i]
		sourceCluster := endpointSlice.Labels[constants.MCSLabelSourceCluster]

		for j := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f, framework.ClusterA, netshootPodList, &endpointSlice.Endpoints[j], sourceCluster,
				nginxServiceClusterB, checkedDomains, *endpointSlice.Endpoints[j].Hostname == "web-0", sourceCluster == clusterAName)
		}
	}

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)
}

//nolint:unparam //  `targetCluster` always receives `framework.ClusterA`.
func verifyEndpointSlices(f *lhframework.Framework, targetCluster framework.ClusterIndex, netshootPodList *corev1.PodList,
	endpointSlices *discovery.EndpointSliceList, service *corev1.Service, verifyCount int, shouldContain bool, localClusterName string,
) {
	count := 0

	for i := range endpointSlices.Items {
		endpointSlice := &endpointSlices.Items[i]
		sourceCluster := endpointSlice.Labels[constants.MCSLabelSourceCluster]

		for j := range endpointSlice.Endpoints {
			verifyEndpointsWithDig(f, targetCluster, netshootPodList, &endpointSlice.Endpoints[j], sourceCluster,
				service, checkedDomains, shouldContain, sourceCluster == localClusterName)
			count++
		}
	}

	Expect(count).To(Equal(verifyCount), "Mismatch in count of IPs to be validated with dig")
}

func verifyEndpointsWithDig(f *lhframework.Framework, targetCluster framework.ClusterIndex, targetPod *corev1.PodList,
	endpoint *discovery.Endpoint, sourceCluster string, service *corev1.Service, domains []string, shouldContain bool, isLocal bool,
) {
	addresses := endpoint.Addresses
	if isLocal {
		addresses, _ = f.GetPodIPs(targetCluster, service, true)
	}

	f.VerifyIPsWithDig(targetCluster, service, targetPod, addresses, domains, *endpoint.Hostname+"."+sourceCluster, shouldContain)
}
