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
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	not       = " not"
	TestLabel = "service-discovery"
)

var checkedDomains = lhframework.CheckedDomains

var _ = Describe("Test Service Discovery Across Clusters", Label(TestLabel), func() {
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
	When("there are no active pods for a service", func() {
		It("should not resolve the service", func() {
			RunServicesPodAvailabilityTest(f)
		})
	})

	When("there are active pods for a service in only one cluster", func() {
		It("should not resolve the service on the cluster without active pods", func() {
			RunServicesPodAvailabilityMultiClusterTest(f)
		})
	})

	When("a pod tries to resolve a service in a specific remote cluster by its cluster name", func() {
		It("should resolve the service on the specified cluster", func() {
			RunServiceDiscoveryClusterNameTest(f)
		})
	})

	When("a pod tries to resolve a service multiple times", func() {
		It("should resolve the service from both the clusters in a round robin fashion", func() {
			RunServiceDiscoveryRoundRobinTest(f)
		})
	})

	When("one of the clusters with a service is not healthy", func() {
		var healthCheckIP, endpointName string

		BeforeEach(func() {
			if len(framework.TestContext.ClusterIDs) < 3 {
				Skip("Only two clusters are deployed and hence skipping the test")
				return
			}

			randomIP := "192.168.1.5"
			healthCheckEnabled := f.GetHealthCheckEnabledInfo(framework.ClusterC)
			if !healthCheckEnabled {
				Skip("Healthcheck is not enabled hence skipping the test")
				return
			}

			endpointName, healthCheckIP = f.GetHealthCheckIPInfo(framework.ClusterC)
			f.SetHealthCheckIP(framework.ClusterC, randomIP, endpointName)
		})

		It("should not resolve that cluster's service IP", func() {
			RunServicesClusterAvailabilityMultiClusterTest(f)
		})

		AfterEach(func() {
			if endpointName != "" {
				f.SetHealthCheckIP(framework.ClusterC, healthCheckIP, endpointName)
			}
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

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, true)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", "", true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, false)

	By(fmt.Sprintf("Re-creating Nginx Service on %q", clusterBName))

	nginxServiceClusterB.ObjectMeta = metav1.ObjectMeta{
		Name:   nginxServiceClusterB.Name,
		Labels: nginxServiceClusterB.Labels,
	}
	nginxServiceClusterB = f.CreateService(framework.KubeClients[framework.ClusterB].CoreV1().Services(f.Namespace), nginxServiceClusterB)
	nginxServiceClusterB, err = f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", "", true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		false, false)
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

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)
	clusterADomain := getClusterDomain(f.Framework, framework.ClusterA, netshootPodList)

	if !framework.TestContext.GlobalnetEnabled {
		f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList,
			[]string{clusterADomain}, "", true)
	}

	f.DeleteService(framework.ClusterA, nginxServiceClusterA.Name)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)

	f.DeleteService(framework.ClusterB, nginxServiceClusterB.Name)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "", "", true)
}

func RunServiceExportTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx ServiceExport on %q", clusterBName))
	f.NewServiceExport(framework.ClusterB, "nginx-demo", f.Namespace)
	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)

	f.DeleteServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 0)

	f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "", "", true)
}

func RunServicesPodAvailabilityTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
	f.SetNginxReplicaSet(framework.ClusterB, 0)
	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, false)
	f.SetNginxReplicaSet(framework.ClusterB, 2)
	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, "",
		true, true)
}

func RunServicesPodAvailabilityMultiClusterTest(f *lhframework.Framework) {
	if len(framework.TestContext.ClusterIDs) < 3 {
		Skip("Only two clusters are deployed and hence skipping the test")
		return
	}

	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.Framework.NewNginxService(framework.ClusterC)

	f.NewServiceExport(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(framework.ClusterC, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	svc, err = f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterB, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterC, nginxServiceClusterC, netshootPodList, checkedDomains,
		"", true)

	f.SetNginxReplicaSet(framework.ClusterC, 0)

	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 1)

	if framework.TestContext.GlobalnetEnabled {
		f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains, "", "1.2.3.4", false)
	} else {
		f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains,
			"", false)
	}

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", true)
	f.SetNginxReplicaSet(framework.ClusterB, 0)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 0)

	if framework.TestContext.GlobalnetEnabled {
		f.VerifyIPWithDig(framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains, "", "1.2.3.4", false)
	} else {
		f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterA, nginxServiceClusterC, netshootPodList, checkedDomains,
			"", false)
	}

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		"", false)
}

func RunServiceDiscoveryClusterNameTest(f *lhframework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterAName))
	f.NewNginxDeployment(framework.ClusterA)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterAName))

	nginxServiceClusterA := f.Framework.NewNginxService(framework.ClusterA)

	f.NewServiceExport(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterA = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterA, 2)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterA.Name, nginxServiceClusterA.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains,
		clusterAName, true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterA, netshootPodList, checkedDomains, clusterAName,
		true, true)

	svc, err = f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList, checkedDomains,
		clusterBName, true)
	verifySRVWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB, netshootPodList, checkedDomains, clusterBName,
		true, true)
}

func RunServiceDiscoveryRoundRobinTest(f *lhframework.Framework) {
	if len(framework.TestContext.ClusterIDs) < 3 {
		Skip("Only two clusters are deployed and hence skipping the test")
		return
	}

	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating an Nginx Deployment on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)

	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.Framework.NewNginxService(framework.ClusterC)

	f.NewServiceExport(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterAName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(framework.ClusterC, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	svc, err = f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterB, nginxServiceClusterB, 2)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 2, 2)

	var serviceIPList []string

	serviceIPClusterB := f.GetServiceIP(framework.ClusterB, nginxServiceClusterB)

	serviceIPList = append(serviceIPList, serviceIPClusterB)

	serviceIPClusterC := f.GetServiceIP(framework.ClusterC, nginxServiceClusterB)

	serviceIPList = append(serviceIPList, serviceIPClusterC)

	verifyRoundRobinWithDig(f.Framework, framework.ClusterA, nginxServiceClusterB.Name, serviceIPList, netshootPodList, checkedDomains)
}

func RunServicesClusterAvailabilityMultiClusterTest(f *lhframework.Framework) {
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]
	clusterCName := framework.TestContext.ClusterIDs[framework.ClusterC]

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterBName))
	f.NewNginxDeployment(framework.ClusterB)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterBName))

	nginxServiceClusterB := f.NewNginxService(framework.ClusterB)

	f.NewServiceExport(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)

	By(fmt.Sprintf("Creating a Netshoot Deployment on %q", clusterCName))

	netshootPodList := f.NewNetShootDeployment(framework.ClusterA)

	svc, err := f.GetService(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterB = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterB, 1)
	f.AwaitEndpointSlices(framework.ClusterB, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterB.Name, nginxServiceClusterB.Namespace, 1, 1)

	By(fmt.Sprintf("Creating an Nginx Deployment on on %q", clusterCName))
	f.NewNginxDeployment(framework.ClusterC)
	By(fmt.Sprintf("Creating a Nginx Service on %q", clusterCName))

	nginxServiceClusterC := f.NewNginxService(framework.ClusterC)

	f.NewServiceExport(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	f.AwaitServiceExportedStatusCondition(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)

	svc, err = f.GetService(framework.ClusterC, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace)
	Expect(err).NotTo(HaveOccurred())

	nginxServiceClusterC = svc
	f.AwaitAggregatedServiceImport(framework.ClusterA, nginxServiceClusterC, 2)
	f.AwaitEndpointSlices(framework.ClusterA, nginxServiceClusterC.Name, nginxServiceClusterC.Namespace, 2, 2)

	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterB, nginxServiceClusterB, netshootPodList,
		checkedDomains, "", true)
	f.VerifyServiceIPWithDig(framework.ClusterA, framework.ClusterC, nginxServiceClusterC, netshootPodList,
		checkedDomains, "", false)
}

//nolint:gocognit,unparam // This really isn't that complex and would be awkward to refactor.
func verifySRVWithDig(f *framework.Framework, srcCluster framework.ClusterIndex, service *corev1.Service, targetPod *corev1.PodList,
	domains []string, clusterName string, withPort, shouldContain bool,
) {
	ports := service.Spec.Ports
	for i := range domains {
		for _, port := range ports {
			cmd := []string{"dig", "+short", "SRV"}

			clusterDNSName := service.Name + "." + f.Namespace + ".svc." + domains[i]

			if clusterName != "" {
				clusterDNSName = clusterName + "." + clusterDNSName
			}

			portName := clusterDNSName

			if withPort {
				portName = strings.ToLower(port.Name+"."+string(port.Protocol)+".") + portName
			}

			cmd = append(cmd, portName)

			op := "is"
			if !shouldContain {
				op += not
			}

			By(fmt.Sprintf("Executing %q to verify SRV record for service %q %q discoverable", strings.Join(cmd, " "),
				service.Name, op))
			framework.AwaitUntil("verify if service Ports is discoverable", func() (interface{}, error) {
				stdout, _, err := f.ExecWithOptions(context.TODO(), &framework.ExecOptions{
					Command:       cmd,
					Namespace:     f.Namespace,
					PodName:       targetPod.Items[0].Name,
					ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
					CaptureStdout: true,
					CaptureStderr: true,
				}, srcCluster)
				if err != nil {
					return nil, err
				}

				return stdout, nil
			}, func(result interface{}) (bool, string, error) {
				var doesContain bool
				if shouldContain {
					doesContain = strings.Contains(result.(string), strconv.Itoa(int(port.Port))) &&
						strings.Contains(result.(string), clusterDNSName)
				} else {
					doesContain = strings.Contains(result.(string), strconv.Itoa(int(port.Port))) ||
						strings.Contains(result.(string), clusterDNSName)
				}

				By(fmt.Sprintf("Validating that port in dig result for SRV Record %q %s %d and the domain name %s %q", result,
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

func verifyRoundRobinWithDig(f *framework.Framework, srcCluster framework.ClusterIndex, serviceName string, serviceIPList []string,
	targetPod *corev1.PodList, domains []string,
) {
	cmd := []string{"dig", "+short"}

	for i := range domains {
		cmd = append(cmd, serviceName+"."+f.Namespace+".svc."+domains[i])
	}

	serviceIPMap := make(map[string]int)

	By(fmt.Sprintf("Executing %q to verify IPs %q for service %q are discoverable in a"+
		" round-robin fashion", strings.Join(cmd, " "), serviceIPList, serviceName))

	var retIPs []string

	for count := 0; count < 10; count++ {
		framework.AwaitUntil("verify if service IP is discoverable", func() (interface{}, error) {
			stdout, _, err := f.ExecWithOptions(context.TODO(), &framework.ExecOptions{
				Command:       cmd,
				Namespace:     f.Namespace,
				PodName:       targetPod.Items[0].Name,
				ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
				CaptureStdout: true,
				CaptureStderr: true,
			}, srcCluster)
			if err != nil {
				return nil, err
			}

			return stdout, nil
		}, func(result interface{}) (bool, string, error) {
			for _, serviceIP := range serviceIPList {
				if strings.Contains(result.(string), serviceIP) {
					serviceIPMap[serviceIP]++
					retIPs = append(retIPs, serviceIP)
					break
				}
			}

			return true, "", nil
		})
	}

	By(fmt.Sprintf("Service IP %q was returned %d times and Service IP %q was returned %d times - "+
		"verifying the difference between them is within the threshold", serviceIPList[0], serviceIPMap[serviceIPList[0]],
		serviceIPList[1], serviceIPMap[serviceIPList[1]]))

	Expect(int(math.Abs(float64(serviceIPMap[serviceIPList[0]]-serviceIPMap[serviceIPList[1]])))).To(BeNumerically("<", 3),
		"Service IPs were not returned in proper round-robin fashion: Expected IPs: %v,"+
			" Returned IPs: %v, IP Counts: %v", serviceIPList, retIPs, serviceIPMap)
}

func getClusterDomain(f *framework.Framework, cluster framework.ClusterIndex, targetPod *corev1.PodList) string {
	/*
		Kubernetes adds --cluster-domain config to all pods' /etc/resolve.conf exactly as follows:
			search <namespace>.svc.cluster.local svc.cluster.local cluster.local <custom-domains>
	*/
	cmd := []string{"cat", "/etc/resolv.conf"}

	if stdout, _, err := f.ExecWithOptions(context.TODO(), &framework.ExecOptions{
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
	// Backup option. Ideally we should never hit this.
	return "cluster" + strconv.Itoa(int(cluster+1)) + ".local"
}
