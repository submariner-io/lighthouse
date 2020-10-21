package endpointslice

import (
	"time"

	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"k8s.io/apimachinery/pkg/util/wait"

	"k8s.io/api/discovery/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/client-go/kubernetes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeKubeClient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

const cluster1EndPointIP1 = "192.168.0.1"
const cluster2EndPointIP1 = "192.169.0.1"
const cluster1HostNamePod1 = "cl1host1"
const cluster2HostNamePod1 = "cl2host1"
const remoteClusterID1 = "west"
const remoteClusterID2 = "south"
const testName1 = "testName1"
const testName2 = "testName2"
const testService1 = "testService1"
const testService2 = "testService2"
const testNS1 = "testNameSpace1"
const testNS2 = "testNameSpace2"

var _ = Describe("EndpointSlice controller", func() {
	t := newEndpointSliceTestDiver()

	When("Endpoints exists in an EndpointSlice ", func() {
		When("IsHealthy is called for the remote clusters", func() {
			It("should return the appropriate response", func() {
				esName := testName1 + remoteClusterID1
				endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
				endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []v1beta1.Endpoint{endPoint1})
				t.createEndpointSlice(testNS1, endpointSlice)
				t.AwaitEndpointSlice(esName, testNS1)
				t.AwaitEpMap(testService1, testNS1)
				Expect(t.controller.IsHealthy(KeyFunc(testService1, testNS1), remoteClusterID1)).To(BeTrue())
			})
		})
	})

	When("When an EndpointSlice does not exists", func() {
		When("IsHealthy is called for the remote clusters", func() {
			It("should return false", func() {
				Expect(t.controller.IsHealthy(KeyFunc(testService1, testNS1), remoteClusterID1)).To(BeFalse())
			})
		})
	})

	When("When an EndpointSlice exists without endpoints", func() {
		When("IsHealthy is called", func() {
			It("should return false", func() {
				esName := testName1 + remoteClusterID1
				endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName, testNS1, []v1beta1.Endpoint{})
				t.createEndpointSlice(testNS1, endpointSlice)
				t.AwaitEndpointSlice(esName, testNS1)
				t.AwaitEpMap(testService1, testNS1)
				Expect(t.controller.IsHealthy(KeyFunc(testService1, testNS1), remoteClusterID1)).To(BeFalse())
			})
		})
	})

	When("When multiple EndpointSlice exists with endpoints", func() {
		When("IsHealthy is called", func() {
			It("should return true", func() {
				esName1 := testName1 + remoteClusterID1
				endPoint1 := t.newEndpoint(cluster1HostNamePod1, cluster1EndPointIP1)
				endpointSlice := t.newEndpointSliceFromEndpoint(testService1, remoteClusterID1, esName1, testNS1, []v1beta1.Endpoint{endPoint1})
				t.createEndpointSlice(testNS1, endpointSlice)
				t.AwaitEndpointSlice(esName1, testNS1)
				t.AwaitEpMap(testService1, testNS1)

				esName2 := testName2 + remoteClusterID2
				endPoint2 := t.newEndpoint(cluster2HostNamePod1, cluster2EndPointIP1)
				endpointSlice2 := t.newEndpointSliceFromEndpoint(testService2, remoteClusterID2, esName2, testNS2, []v1beta1.Endpoint{endPoint2})
				t.createEndpointSlice(testNS2, endpointSlice2)
				t.AwaitEndpointSlice(esName2, testNS2)
				t.AwaitEpMap(testService2, testNS2)

				Expect(t.controller.IsHealthy(KeyFunc(testService1, testNS1), remoteClusterID1)).To(BeTrue())
				Expect(t.controller.IsHealthy(KeyFunc(testService2, testNS2), remoteClusterID2)).To(BeTrue())
			})
		})
	})

})

type endpointSliceTestDriver struct {
	controller *Controller
	kubeClient kubernetes.Interface
	epMap      *Map
}

func newEndpointSliceTestDiver() *endpointSliceTestDriver {
	t := &endpointSliceTestDriver{}

	BeforeEach(func() {
		t.kubeClient = fakeKubeClient.NewSimpleClientset()
		t.epMap = NewMap()
		t.controller = NewController(t.epMap)
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
	_, err := t.kubeClient.DiscoveryV1beta1().EndpointSlices(namespace).Create(endpointSlice)
	Expect(err).To(Succeed())
}

func (t *endpointSliceTestDriver) newEndpointSliceFromEndpoint(serviceName, remoteCluster, esName,
	esNs string, endPoints []v1beta1.Endpoint) *v1beta1.EndpointSlice {
	return &v1beta1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      esName,
			Namespace: esNs,
			Labels: map[string]string{
				v1beta1.LabelManagedBy:           lhconstants.LabelValueManagedBy,
				lhconstants.LabelSourceName:      serviceName,
				lhconstants.LabelSourceCluster:   remoteCluster,
				lhconstants.LabelSourceNamespace: esNs,
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

func (t *endpointSliceTestDriver) AwaitEndpointSlice(name, nameSpace string) {
	var found *v1beta1.EndpointSlice

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		var err error
		found, err = t.kubeClient.DiscoveryV1beta1().EndpointSlices(nameSpace).Get(name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			return false, err
		}
		return true, nil
	})

	if err == wait.ErrWaitTimeout {
		if found == nil {
			Fail("EndpointSlice not found")
		}
	} else {
		Expect(err).To(Succeed())
	}
}

func (t *endpointSliceTestDriver) AwaitEpMap(name, nameSpace string) {
	var found *endpointInfo

	err := wait.Poll(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		found = t.epMap.Get(KeyFunc(name, nameSpace))
		if found == nil {
			return false, nil
		}
		return true, nil
	})

	if err == wait.ErrWaitTimeout {
		if found == nil {
			Fail("Endpoint not found in Map")
		}
	} else {
		Expect(err).To(Succeed())
	}
}
