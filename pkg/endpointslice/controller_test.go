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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/endpointslice"
	"k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	fakeKubeClient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
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
)

var _ = Describe("EndpointSlice controller", func() {
	t := newEndpointSliceTestDiver()

	When("a service has a valid endpoint", func() {
		When("IsHealthy is called for the service with a valid cluster", func() {
			It("should return true", func() {
				esName := testName1 + remoteClusterID1
				endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
				endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []v1beta1.Endpoint{endPoint1})
				t.createEndpointSlice(testNS1, endpointSlice)
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
		It("should return false", func() {
			esName := testName1 + remoteClusterID1
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []v1beta1.Endpoint{})
			t.createEndpointSlice(testNS1, endpointSlice)
			t.awaitNotIsHealthy(testService1, testNS1, remoteClusterID1)
		})
	})

	When("a service exists in multiple clusters with valid endpoints", func() {
		When("IsHealthy is called for each cluster", func() {
			It("should return true", func() {
				esName1 := testName1 + remoteClusterID1
				endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
				endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName1, testNS1, []v1beta1.Endpoint{endPoint1})
				t.createEndpointSlice(testNS1, endpointSlice)

				esName2 := testName2 + remoteClusterID2
				endPoint2 := t.newEndpoint(cluster2HostNamePod1, cluster2EndPointIP1)
				endpointSlice2 := t.newEndpointSliceFromEndpoint(testService2, remoteClusterID2, esName2, testNS2, []v1beta1.Endpoint{endPoint2})
				t.createEndpointSlice(testNS2, endpointSlice2)

				t.awaitIsHealthy(testService1, testNS1, remoteClusterID1)
				t.awaitIsHealthy(testService2, testNS2, remoteClusterID2)
			})
		})
	})

	When("IsHealthy is called for a non-existent cluster", func() {
		It("should return false", func() {
			esName1 := testName1 + remoteClusterID1
			endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
			endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName1, testNS1, []v1beta1.Endpoint{endPoint1})
			t.createEndpointSlice(testNS1, endpointSlice)

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
		t.epMap = endpointslice.NewMap()
		t.controller = endpointslice.NewController(t.epMap)
		t.controller.NewClientset = func(c *rest.Config) (kubernetes.Interface, error) {
			return t.kubeClient, nil
		}

		Expect(t.controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		t.controller.Stop()
	})

	return t
}

func (t *endpointSliceTestDriver) createEndpointSlice(namespace string, endpointSlice *v1beta1.EndpointSlice) {
	_, err := t.kubeClient.DiscoveryV1beta1().EndpointSlices(namespace).Create(context.TODO(), endpointSlice, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (t *endpointSliceTestDriver) newEndpointSliceFromEndpoint(serviceName, remoteCluster, esName,
	esNs string, endPoints []v1beta1.Endpoint) *v1beta1.EndpointSlice {
	return &v1beta1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      esName,
			Namespace: esNs,
			Labels: map[string]string{
				v1beta1.LabelManagedBy:            lhconstants.LabelValueManagedBy,
				lhconstants.MCSLabelServiceName:   serviceName,
				lhconstants.MCSLabelSourceCluster: remoteCluster,
				lhconstants.LabelSourceNamespace:  esNs,
			},
		},
		Endpoints: endPoints,
	}
}

func (t *endpointSliceTestDriver) newEndpoint(hostname, ipaddress string) v1beta1.Endpoint {
	return v1beta1.Endpoint{
		Addresses:  []string{ipaddress},
		Conditions: v1beta1.EndpointConditions{},
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
