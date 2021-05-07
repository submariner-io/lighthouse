/*
© 2020 Red Hat, Inc. and others

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
package gateway_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/lighthouse/pkg/gateway"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

const localClusterID = "east"
const remoteClusterID1 = "west"
const remoteClusterID2 = "south"

var _ = Describe("Gateway controller", func() {
	t := newTestDiver()

	When("an active Gateway is created", func() {
		When("LocalClusterID is called", func() {
			It("should return the correct local cluster ID", func() {
				t.localClusterIDValidationTest(localClusterID)
			})
		})

		When("LocalClusterID is called after the ID is updated", func() {
			It("should return the new cluster ID", func() {
				t.localClusterIDUpdateValidationTest(localClusterID, remoteClusterID1)
			})
		})

		When("IsConnected is called for the local cluster ID", func() {
			It("should return true", func() {
				t.awaitIsNotConnected(localClusterID)
				t.createGateway()
				t.awaitIsConnected(localClusterID)
			})
		})
	})

	When("an active Gateway is created with remote cluster connections", func() {
		BeforeEach(func() {
			t.addGatewayStatusConnection(remoteClusterID1, "connected")
			t.addGatewayStatusConnection(remoteClusterID2, "connecting")
		})

		When("IsConnected is called for the remote clusters", func() {
			It("should return the appropriate response", func() {
				t.createGateway()
				t.awaitIsConnected(remoteClusterID1)
				t.awaitIsConnected(localClusterID)
				t.awaitIsNotConnected(remoteClusterID2)
			})
		})
	})

	When("the connection status for remote clusters are updated for an active Gateway", func() {
		When("IsConnected is called for the remote clusters", func() {
			It("should return the appropriate response", func() {
				t.createGateway()
				t.awaitIsConnected(localClusterID)

				t.addGatewayStatusConnection(remoteClusterID1, "connected")
				t.updateGateway()
				t.awaitIsConnected(remoteClusterID1)

				t.addGatewayStatusConnection(remoteClusterID1, "error")
				t.addGatewayStatusConnection(remoteClusterID2, "connected")
				t.updateGateway()
				t.awaitIsNotConnected(remoteClusterID1)
				t.awaitIsConnected(remoteClusterID2)

				t.addGatewayStatusConnection(remoteClusterID1, "connected")
				t.addGatewayStatusConnection(remoteClusterID2, "error")
				t.updateGateway()
				t.awaitIsConnected(remoteClusterID1)
				t.awaitIsNotConnected(remoteClusterID2)
			})
		})
	})

	When("a passive Gateway is created", func() {
		BeforeEach(func() {
			Expect(unstructured.SetNestedField(t.gatewayObj.Object, "passive", "status", "haStatus")).To(Succeed())
		})

		When("LocalClusterID is called", func() {
			It("should return the correct local cluster ID", func() {
				t.localClusterIDValidationTest(localClusterID)
			})
		})

		When("LocalClusterID is called after the ID is updated", func() {
			It("should return the new cluster ID", func() {
				t.localClusterIDUpdateValidationTest(localClusterID, remoteClusterID1)
			})
		})

		When("IsConnected is called for the local cluster ID", func() {
			It("should return true", func() {
				t.createGateway()
				t.awaitIsConnected(localClusterID)
			})
		})
	})

	When("IsConnected is called for a non-existent cluster ID", func() {
		It("should return false", func() {
			Expect(t.controller.IsConnected(remoteClusterID1)).To(BeFalse())
		})
	})

	When("the Gateway resource doesn't exist", func() {
		BeforeEach(func() {
			t.gatewayReactor.SetFailOnList(errors.NewNotFound(schema.GroupResource{}, ""))
		})

		When("IsConnected is called", func() {
			It("should return true", func() {
				t.awaitIsConnected(localClusterID)
				t.awaitIsConnected(remoteClusterID1)
			})
		})
	})
})

type testDriver struct {
	controller     *gateway.Controller
	dynClient      *fakeClient.FakeDynamicClient
	gatewayClient  dynamic.ResourceInterface
	gatewayReactor *fake.FailingReactor
	gatewayObj     *unstructured.Unstructured
}

func newTestDiver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.dynClient = fakeClient.NewSimpleDynamicClient(runtime.NewScheme())

		t.gatewayClient = t.dynClient.Resource(schema.GroupVersionResource{
			Group:    "submariner.io",
			Version:  "v1",
			Resource: "gateways",
		}).Namespace(corev1.NamespaceAll)

		t.gatewayReactor = fake.NewFailingReactorForResource(&t.dynClient.Fake, "gateways")
		t.gatewayObj = newGateway()
	})

	JustBeforeEach(func() {
		t.controller = gateway.NewController()
		t.controller.NewClientset = func(c *rest.Config) (dynamic.Interface, error) {
			return t.dynClient, nil
		}

		Expect(t.controller.Start(&rest.Config{})).To(Succeed())
	})

	AfterEach(func() {
		t.controller.Stop()
	})

	return t
}

func (t *testDriver) awaitIsConnected(clusterID string) {
	Eventually(func() bool {
		return t.controller.IsConnected(clusterID)
	}, 5).Should(BeTrue())
}

func (t *testDriver) awaitIsNotConnected(clusterID string) {
	Eventually(func() bool {
		return t.controller.IsConnected(clusterID)
	}, 5).Should(BeFalse())
}

func (t *testDriver) localClusterIDValidationTest(localClusterID string) {
	t.createGateway()
	t.awaitValidLocalClusterID(localClusterID)
}

func (t *testDriver) localClusterIDUpdateValidationTest(originalLocalClusterID, originalRemoteClusterID string) {
	// First validate for current state
	t.createGateway()
	t.awaitValidLocalClusterID(originalLocalClusterID)

	// Second change remote to be the local and validate for new state
	t.setGatewayLocalClusterID(originalRemoteClusterID)
	t.updateGateway()
	t.awaitValidLocalClusterID(originalRemoteClusterID)
}

func (t *testDriver) awaitValidLocalClusterID(clusterID string) {
	Eventually(func() string {
		return t.controller.LocalClusterID()
	}, 5).Should(Equal(clusterID))
}

func (t *testDriver) createGateway() {
	_, err := t.gatewayClient.Create(context.TODO(), t.gatewayObj, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (t *testDriver) updateGateway() {
	_, err := t.gatewayClient.Update(context.TODO(), t.gatewayObj, metav1.UpdateOptions{})
	Expect(err).To(Succeed())
}

func (t *testDriver) setGatewayLocalClusterID(clusterID string) {
	Expect(unstructured.SetNestedField(t.gatewayObj.Object, clusterID, "status", "localEndpoint", "cluster_id")).To(Succeed())
}

func (t *testDriver) addGatewayStatusConnection(clusterID, status string) *unstructured.Unstructured {
	current, _, err := unstructured.NestedSlice(t.gatewayObj.Object, "status", "connections")
	Expect(err).To(Succeed())

	conn := map[string]interface{}{}
	Expect(unstructured.SetNestedField(conn, status, "status")).To(Succeed())
	Expect(unstructured.SetNestedField(conn, clusterID, "endpoint", "cluster_id")).To(Succeed())

	Expect(unstructured.SetNestedSlice(t.gatewayObj.Object, append(current, conn), "status", "connections")).To(Succeed())

	return t.gatewayObj
}

func newGateway() *unstructured.Unstructured {
	gw := &unstructured.Unstructured{}
	gw.SetName("test-gateway")
	Expect(unstructured.SetNestedField(gw.Object, localClusterID, "status", "localEndpoint", "cluster_id")).To(Succeed())
	Expect(unstructured.SetNestedField(gw.Object, "active", "status", "haStatus")).To(Succeed())

	return gw
}

func init() {
	klog.InitFlags(nil)
}

func TestGateway(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gateway Suite")
}
