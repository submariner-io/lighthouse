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

package endpointslice_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/endpointslice"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	fakeKubeClient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	cluster1EndPointIP1  = "192.168.0.1"
	cluster2EndPointIP1  = "192.169.0.1"
	cluster1HostNamePod1 = "cl1host1"
	cluster2HostNamePod1 = "cl2host1"
	remoteClusterID1     = "west"
	remoteClusterID2     = "south"
	testName1            = "testName1"
	testName2            = "testName2"
	testService1         = "testService1"
	testService2         = "testService2"
	testNS1              = "testNameSpace1"
	testNS2              = "testNameSpace2"
	localClusterID       = "local"
)

var _ = Describe("EndpointSlice controller", func() {
	t := newEndpointSliceTestDiver()

	When("a service has a valid endpoint", func() {
		BeforeEach(func() {
			esName := testName1 + remoteClusterID1
			endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []discovery.Endpoint{endPoint1})
			t.createEndpointSlice(testNS1, endpointSlice)
		})

		When("IsHealthy is called for the service with a valid cluster", func() {
			It("should return true", func() {
				t.awaitIsHealthy(testService1, testNS1, remoteClusterID1)
			})
		})
	})

	When("IsHealthy is called for a non-existent service", func() {
		It("should return false", func() {
			Expect(t.controller.IsHealthy(testService1, testNS1, remoteClusterID1)).To(BeFalse())
		})
	})

	When("IsHealthy is called for a service with no endpoints", func() {
		BeforeEach(func() {
			esName := testName1 + remoteClusterID1
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []discovery.Endpoint{})
			t.createEndpointSlice(testNS1, endpointSlice)
		})

		It("should return false", func() {
			t.awaitNotIsHealthy(testService1, testNS1, remoteClusterID1)
		})
	})

	When("a service exists in multiple clusters with valid endpoints", func() {
		BeforeEach(func() {
			esName1 := testName1 + remoteClusterID1
			endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName1, testNS1, []discovery.Endpoint{endPoint1})
			t.createEndpointSlice(testNS1, endpointSlice)

			esName2 := testName2 + remoteClusterID2
			endPoint2 := t.newEndpoint(cluster2HostNamePod1, cluster2EndPointIP1)
			endpointSlice2 := t.newEndpointSliceFromEndpoint(testService2, remoteClusterID2, esName2, testNS2, []discovery.Endpoint{endPoint2})
			t.createEndpointSlice(testNS2, endpointSlice2)
		})

		When("IsHealthy is called for each cluster", func() {
			It("should return true", func() {
				t.awaitIsHealthy(testService1, testNS1, remoteClusterID1)
				t.awaitIsHealthy(testService2, testNS2, remoteClusterID2)
			})
		})
	})

	When("IsHealthy is called for a non-existent cluster", func() {
		BeforeEach(func() {
			esName1 := testName1 + remoteClusterID1
			endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName1, testNS1, []discovery.Endpoint{endPoint1})
			t.createEndpointSlice(testNS1, endpointSlice)
		})

		It("should return false", func() {
			t.awaitNotIsHealthy(testService1, testNS1, "randomcluster")
		})
	})
})

type endpointSliceTestDriver struct {
	controller *endpointslice.Controller
	kubeClient kubernetes.Interface
	epMap      *endpointslice.Map
}

func newEndpointSliceTestDiver() *endpointSliceTestDriver {
	t := &endpointSliceTestDriver{}

	BeforeEach(func() {
		t.kubeClient = fakeKubeClient.NewSimpleClientset()
		t.epMap = endpointslice.NewMap(localClusterID, t.kubeClient)
		t.controller = endpointslice.NewController(t.epMap)
		t.controller.NewClientset = func(c *rest.Config) (kubernetes.Interface, error) {
			return t.kubeClient, nil
		}
	})

	JustBeforeEach(func() {
		Expect(t.controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		t.controller.Stop()
	})

	return t
}

func (t *endpointSliceTestDriver) createEndpointSlice(namespace string, endpointSlice *discovery.EndpointSlice) {
	_, err := t.kubeClient.DiscoveryV1().EndpointSlices(namespace).Create(context.TODO(), endpointSlice, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (t *endpointSliceTestDriver) newEndpointSliceFromEndpoint(serviceName, remoteCluster, esName,
	esNs string, endPoints []discovery.Endpoint,
) *discovery.EndpointSlice {
	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      esName,
			Namespace: esNs,
			Labels: map[string]string{
				discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				mcsv1a1.LabelServiceName:        serviceName,
				constants.MCSLabelSourceCluster: remoteCluster,
				constants.LabelSourceNamespace:  esNs,
			},
		},
		Endpoints: endPoints,
	}
}

func (t *endpointSliceTestDriver) newEndpoint(hostname, ipaddress string) discovery.Endpoint {
	return discovery.Endpoint{
		Addresses:  []string{ipaddress},
		Conditions: discovery.EndpointConditions{},
		Hostname:   &hostname,
	}
}

func (t *endpointSliceTestDriver) awaitIsHealthy(name, nameSpace, clusterID string) {
	Eventually(func() bool {
		return t.controller.IsHealthy(name, nameSpace, clusterID)
	}, 5).Should(BeTrue())
}

func (t *endpointSliceTestDriver) awaitNotIsHealthy(name, nameSpace, clusterID string) {
	Consistently(func() bool {
		return t.controller.IsHealthy(name, nameSpace, clusterID)
	}, 500*time.Millisecond).Should(BeFalse())
}
