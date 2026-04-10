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
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	lhframework "github.com/submariner-io/lighthouse/test/e2e/framework"
	"github.com/submariner-io/lighthouse/test/e2e/labels"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var checkedDomains = lhframework.CheckedDomains

var _ = Describe("Test Service Discovery Across Clusters", Label(labels.ServiceDiscovery), func() {
	f := lhframework.NewFramework("discovery")

	BeforeEach(func(ctx context.Context) {
		if lhframework.IsClusterSetIPEnabled(ctx) {
			Skip("The clusterset IP feature is enabled globally - skipping the test")
		}
	})

	When("a pod tries to resolve a service in a remote cluster", func() {
		It("should be able to discover the remote service successfully", func(ctx context.Context) {
			RunServiceDiscoveryTest(ctx, f)
		})
	})

	When("a pod tries to resolve a service which is present locally and in a remote cluster", func() {
		It("should resolve the local service", func(ctx context.Context) {
			RunServiceDiscoveryLocalTest(ctx, f)
		})
	})

	When("service export is created before the service", func() {
		It("should resolve the service", func(ctx context.Context) {
			RunServiceExportTest(ctx, f)
		})
	})
	When("there are no active pods for a service", func() {
		It("should not resolve the service", func(ctx context.Context) {
			RunServicesPodAvailabilityTest(ctx, f)
		})
	})

	When("there are active pods for a service in only one cluster", func() {
		It("should not resolve the service on the cluster without active pods", func(ctx context.Context) {
			RunServicesPodAvailabilityMultiClusterTest(ctx, f)
		})
	})

	When("a pod tries to resolve a service in a specific remote cluster by its cluster name", func() {
		It("should resolve the service on the specified cluster", func(ctx context.Context) {
			RunServiceDiscoveryClusterNameTest(ctx, f)
		})
	})

	When("a pod tries to resolve a service multiple times", func() {
		It("should resolve the service from both the clusters in a round robin fashion", func(ctx context.Context) {
			RunServiceDiscoveryRoundRobinTest(ctx, f)
		})
	})

	When("one of the clusters with a service is not healthy", func() {
		var healthCheckIP, endpointName string

		BeforeEach(func(ctx context.Context) {
			if len(framework.TestContext.ClusterIDs) < 3 {
				Skip("Only two clusters are deployed and hence skipping the test")
				return
			}

			randomIP := "192.168.1.5"

			healthCheckEnabled := f.GetHealthCheckEnabledInfo(ctx, framework.ClusterC)
			if !healthCheckEnabled {
				Skip("Healthcheck is not enabled hence skipping the test")
				return
			}

			endpointName, healthCheckIP = f.GetHealthCheckIPInfo(ctx, framework.ClusterC)
			f.SetHealthCheckIP(ctx, framework.ClusterC, randomIP, endpointName)

			DeferCleanup(func(ctx context.Context) {
				if endpointName != "" {
					f.SetHealthCheckIP(ctx, framework.ClusterC, healthCheckIP, endpointName)
				}
			})
		})

		It("should not resolve that cluster's service IP", func(ctx context.Context) {
			RunServicesClusterAvailabilityMultiClusterTest(ctx, f)
		})
	})
})

func RunServiceDiscoveryTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, true)

	f.DeleteService(ctx, framework.ClusterB, nginxServiceClusterB.Name)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", "", true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, false)

	framework.By(fmt.Sprintf("Re-creating Nginx Service on %q", clusterBName))

	nginxServiceClusterB.ObjectMeta = metav1.ObjectMeta{
		Name:   nginxServiceClusterB.Name,
		Labels: nginxServiceClusterB.Labels,
	}
	nginxServiceClusterB = f.CreateService(ctx, framework.KubeClients[framework.ClusterB].CoreV1().Services(f.Namespace), nginxServiceClusterB)
	nginxServiceClusterB, err = f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", "", true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, false)
}

func RunServiceDiscoveryLocalTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(ctx, framework.ClusterA)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))
	// don't need ServiceExport for local service
	nginxServiceClusterA := f.Framework.NewNginxService(ctx, framework.ClusterA)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)
	clusterADomain := getClusterDomain(ctx, f.Framework, framework.ClusterA, netshootPodList)

	if !framework.TestContext.GlobalnetEnabled {
		f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList,
			[]string{clusterADomain}, "", true)
	}

	f.DeleteService(ctx, framework.ClusterA, nginxServiceClusterA.Name)

	svc, err := f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 0)

	f.DeleteService(ctx, framework.ClusterB, nginxServiceClusterB.Name)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "", "", true)
}

func RunServiceExportTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx ServiceExport on %q", clusterBName))
	f.NewServiceExport(ctx, framework.ClusterB, "nginx-demo", f.Namespace)
	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "", "", true)
}

func RunServicesPodAvailabilityTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)
	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
	f.SetNginxReplicaSet(ctx, framework.ClusterB, 0)
	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	f.SetNginxReplicaSet(ctx, framework.ClusterB, 2)
	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
}

func RunServicesPodAvailabilityMultiClusterTest(ctx context.Context, f *lhframework.Framework) {
	if len(framework.TestContext.ClusterIDs) < 3 {
		Skip("Only two clusters are deployed and hence skipping the test")
		return
	}

	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(ctx, framework.ClusterC)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.Framework.NewNginxService(ctx, framework.ClusterC)

	f.NewServiceExport(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterC, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	svc, err = f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterB, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterC, nginxServiceClusterC, netshootPodList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(ctx, framework.ClusterC, 0)

	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 1)

	if framework.TestContext.GlobalnetEnabled {
		f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains, "", "1.2.3.4", false)
	} else {
		f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains,
			"", false)
	}

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	f.SetNginxReplicaSet(ctx, framework.ClusterB, 0)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 0)

	if framework.TestContext.GlobalnetEnabled {
		f.VerifyIPWithDig(ctx, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains, "", "1.2.3.4", false)
	} else {
		f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains,
			"", false)
	}

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServiceDiscoveryClusterNameTest(ctx context.Context, f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(ctx, framework.ClusterA)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))

	nginxServiceClusterA := f.Framework.NewNginxService(ctx, framework.ClusterA)

	f.NewServiceExport(ctx, framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterA = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterA, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		clusterAName, true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains, clusterAName,
		true, true)

	svc, err = f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		clusterBName, true)
	verifySRVWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, clusterBName,
		true, true)
}

func RunServiceDiscoveryRoundRobinTest(ctx context.Context, f *lhframework.Framework) {
	if len(framework.TestContext.ClusterIDs) < 3 {
		Skip("Only two clusters are deployed and hence skipping the test")
		return
	}

	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(ctx, framework.ClusterC)

	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.Framework.NewNginxService(ctx, framework.ClusterC)

	f.NewServiceExport(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterC, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	svc, err = f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterB, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	serviceIPClusterB := f.GetServiceIP(ctx, framework.ClusterB, nginxServiceClusterB, corev1.IPFamilyUnknown)
	serviceIPClusterC := f.GetServiceIP(ctx, framework.ClusterC, nginxServiceClusterC, corev1.IPFamilyUnknown)

	verifyRoundRobinWithDig(ctx, f.Framework, framework.ClusterA, nginxServiceClusterB.Name, []string{serviceIPClusterB, serviceIPClusterC},
		netshootPodList, checkedDomains)
}

func RunServicesClusterAvailabilityMultiClusterTest(ctx context.Context, f *lhframework.Framework) {
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(ctx, framework.ClusterB)
	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(ctx, framework.ClusterB)

	f.NewServiceExport(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	framework.By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterCName))

	netshootPodList := f.NewNetShootDeployment(ctx, framework.ClusterA)

	svc, err := f.GetService(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	framework.By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(ctx, framework.ClusterC)
	framework.By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.NewNginxService(ctx, framework.ClusterC)

	f.NewServiceExport(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	f.AwaitServiceExportedStatusCondition(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	svc, err = f.GetService(ctx, framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(ctx, framework.ClusterA, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(ctx, framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList,
		checkedDomains, "", true)
	f.VerifyServiceIPWithDig(ctx, framework.ClusterA, framework.ClusterC, nginxServiceClusterC, netshootPodList,
		checkedDomains, "", false)
}

func verifySRVWithDig(ctx context.Context, f *framework.Framework, srcCluster framework.ClusterIndex, service *corev1.Service,
	targetPod *corev1.PodList, domains []string, clusterName string, withPort, shouldContain bool,
) {
	ports := service.Spec.Ports
	for i := range domains {
		for _, port := range ports {
			cmd := make([]string, 0, 4)
			cmd = append(cmd, "dig", "+short", "SRV")

			clusterDNSName := lhframework.BuildServiceDNSName(clusterName, service.Name, f.Namespace, domains[i])

			portName := clusterDNSName

			if withPort {
				portName = strings.ToLower(port.Name+"."+string(port.Protocol)+".") + portName
			}

			cmd = append(cmd, portName)

			op := "is"
			if !shouldContain {
				op += " not"
			}

			framework.By(fmt.Sprintf("Executing %q to verify SRV record for service %q %q discoverable", strings.Join(cmd, " "),
				service.Name, op))
			framework.AwaitUntil(ctx, "verify if service Ports is discoverable", func(ctx context.Context) (string, error) {
				stdout, _, err := f.ExecWithOptions(ctx, &framework.ExecOptions{
					Command:       cmd,
					Namespace:     f.Namespace,
					PodName:       targetPod.Items[0].Name,
					ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
					CaptureStdout: true,
					CaptureStderr: true,
				}, srcCluster)
				if err != nil {
					return "", err
				}

				return stdout, nil
			}, func(result string) (bool, string, error) {
				var doesContain bool
				if shouldContain {
					doesContain = strings.Contains(result, strconv.Itoa(int(port.Port))) &&
						strings.Contains(result, clusterDNSName)
				} else {
					doesContain = strings.Contains(result, strconv.Itoa(int(port.Port))) ||
						strings.Contains(result, clusterDNSName)
				}

				framework.By(fmt.Sprintf("Validating that port in dig result for SRV Record %q %s %d and the domain name %s %q", result,
					op, port.Port, op, clusterDNSName))

				if doesContain && !shouldContain {
					return false, fmt.Sprintf("expected execution result %q not to contain %d", result, port.Port), nil
				}

				if !doesContain && shouldContain {
					return false, fmt.Sprintf("expected execution result %q to contain %q", result, port.Port), nil
				}

				return true, "", nil
			})
		}
	}
}

func verifyRoundRobinWithDig(ctx context.Context, f *framework.Framework, srcCluster framework.ClusterIndex, serviceName string,
	serviceIPList []string, targetPod *corev1.PodList, domains []string,
) {
	cmd := make([]string, 0, 2+len(domains))
	cmd = append(cmd, "dig", "+short")

	for i := range domains {
		cmd = append(cmd, lhframework.BuildServiceDNSName("", serviceName, f.Namespace, domains[i]))
	}

	serviceIPMap := make(map[string]int)

	framework.By(fmt.Sprintf("Executing %q to verify IPs %q for service %q are discoverable in a"+
		" round-robin fashion", strings.Join(cmd, " "), serviceIPList, serviceName))

	var retIPs []string

	for range 10 {
		framework.AwaitUntil(ctx, "verify if service IP is discoverable", func(ctx context.Context) (string, error) {
			stdout, _, err := f.ExecWithOptions(ctx, &framework.ExecOptions{
				Command:       cmd,
				Namespace:     f.Namespace,
				PodName:       targetPod.Items[0].Name,
				ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
				CaptureStdout: true,
				CaptureStderr: true,
			}, srcCluster)
			if err != nil {
				return "", err
			}

			return stdout, nil
		}, func(result string) (bool, string, error) {
			for _, serviceIP := range serviceIPList {
				if strings.Contains(result, serviceIP) {
					serviceIPMap[serviceIP]++
					retIPs = append(retIPs, serviceIP)

					break
				}
			}

			return true, "", nil
		})
	}

	framework.By(fmt.Sprintf("Service IP %q was returned %d times and Service IP %q was returned %d times - "+
		"verifying the difference between them is within the threshold", serviceIPList[0], serviceIPMap[serviceIPList[0]],
		serviceIPList[1], serviceIPMap[serviceIPList[1]]))

	Expect(int(math.Abs(float64(serviceIPMap[serviceIPList[0]]-serviceIPMap[serviceIPList[1]])))).To(BeNumerically("<", 3),
		"Service IPs were not returned in proper round-robin fashion: Expected IPs: %v,"+
			" Returned IPs: %v, IP Counts: %v", serviceIPList, retIPs, serviceIPMap)
}

func getClusterDomain(ctx context.Context, f *framework.Framework, cluster framework.ClusterIndex, targetPod *corev1.PodList) string {
	/*
		Kubernetes adds --cluster-domain config to all pods' /etc/resolve.conf exactly as follows:
			search <namespace>.svc.cluster.local svc.cluster.local cluster.local <custom-domains>
	*/
	cmd := []string{"cat", "/etc/resolv.conf"}

	if stdout, _, err := f.ExecWithOptions(ctx, &framework.ExecOptions{
		Command:       cmd,
		Namespace:     f.Namespace,
		PodName:       targetPod.Items[0].Name,
		ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
		CaptureStdout: true,
		CaptureStderr: true,
	}, cluster); err == nil {
		for line := range strings.SplitSeq(stdout, "\n") {
			if strings.Contains(line, "search") {
				ss := strings.Split(line, " ")
				return ss[3]
			}
		}
	}
	// Backup option. Ideally we should never hit this.
	return "cluster" + strconv.Itoa(int(cluster+1)) + ".local"
}
