package gateway_test

import (
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

func (t *testDriver) createGateway() {
	_, err := t.gatewayClient.Create(t.gatewayObj, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func (t *testDriver) updateGateway() {
	_, err := t.gatewayClient.Update(t.gatewayObj, metav1.UpdateOptions{})
	Expect(err).To(Succeed())
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

func TestGatewayt(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gateway Suite")
}
