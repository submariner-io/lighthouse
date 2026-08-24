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

package controller_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/ipam"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	k8snet "k8s.io/utils/net"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

const (
	clusterID1       = "east"
	clusterID2       = "west"
	serviceName      = "nginx"
	serviceNamespace = "service-ns"
	ipV4ServiceIP1   = "10.253.9.1"
	ipV4ServiceIP2   = "10.253.10.1"
	globalIP1        = "242.254.1.1"
	globalIP2        = "242.254.1.2"
	globalIP3        = "242.254.1.3"
	epIP1            = "192.168.5.1"
	epIP2            = "192.168.5.2"
	epIP3            = "10.253.6.1"
	epIP4            = "10.253.6.2"
)

var (
	nodeName = "my-node"
	hostName = "my-host"
	host1    = "host1"
	host2    = "host2"

	port1 = mcsv1b1.ServicePort{
		Name:     "http",
		Protocol: corev1.ProtocolTCP,
		Port:     8080,
	}

	port2 = mcsv1b1.ServicePort{
		Name:     "https",
		Protocol: corev1.ProtocolTCP,
		Port:     8443,
	}

	port3 = mcsv1b1.ServicePort{
		Name:        "POP3",
		Protocol:    corev1.ProtocolUDP,
		Port:        110,
		AppProtocol: new("smtp"),
	}
)

func init() {
	kzerolog.InitK8sLogging()

	err := mcsv1b1.Install(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Controller Suite")
}

type cluster struct {
	agentSpec                  controller.AgentSpecification
	localDynClient             dynamic.Interface
	localDynClientFake         *k8stesting.Fake
	localServiceImportClient   dynamic.NamespaceableResourceInterface
	localIngressIPClient       dynamic.ResourceInterface
	localEndpointSliceClient   dynamic.ResourceInterface
	localServiceImportReactor  *fake.FailingReactor
	agentController            *controller.Controller
	service                    *corev1.Service
	expectedClusterIPEndpoints []discovery.Endpoint
	serviceExport              *mcsv1b1.ServiceExport
	serviceEndpointSlices      []discovery.EndpointSlice
	clusterID                  string
	headlessEndpointAddresses  [][]discovery.Endpoint
	supportedIPFamilies        []corev1.IPFamily
	verifyImportReadyCondition func(c *metav1.Condition)
}

type testDriver struct {
	cluster1                        cluster
	cluster2                        cluster
	brokerServiceImportClient       dynamic.NamespaceableResourceInterface
	brokerEndpointSliceClient       dynamic.ResourceInterface
	brokerEndpointSliceReactor      *fake.FailingReactor
	stopCh                          chan struct{}
	syncerConfig                    *broker.SyncerConfig
	doStart                         bool
	useClusterSetIP                 bool
	ipPool                          *ipam.IPPool
	brokerServiceImportReactor      *fake.FailingReactor
	aggregatedServicePorts          []mcsv1b1.ServicePort
	aggregatedSessionAffinity       corev1.ServiceAffinity
	aggregatedSessionAffinityConfig *corev1.SessionAffinityConfig
	aggregatedTrafficDistribution   *string
	aggregatedInternalTrafficPolicy *corev1.ServiceInternalTrafficPolicy
	aggregatedIPFamilies            []corev1.IPFamily
}

func newTestDiver(ctx context.Context) *testDriver {
	syncerScheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(syncerScheme)).To(Succeed())
	Expect(discovery.AddToScheme(syncerScheme)).To(Succeed())
	Expect(mcsv1b1.Install(syncerScheme)).To(Succeed())

	syncerScheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "submariner.io",
		Version: "v1",
		Kind:    "GlobalIngressIPList",
	}, &unstructured.UnstructuredList{})

	brokerClient := dynamicfake.NewSimpleDynamicClient(syncerScheme)
	fake.AddBasicReactors(&brokerClient.Fake)

	t := &testDriver{
		aggregatedServicePorts:    []mcsv1b1.ServicePort{port1, port2},
		aggregatedSessionAffinity: corev1.ServiceAffinityNone,
		aggregatedIPFamilies:      nil,
		cluster1: cluster{
			clusterID:                  clusterID1,
			supportedIPFamilies:        nil,
			verifyImportReadyCondition: assertImportReady,
			agentSpec: controller.AgentSpecification{
				ClusterID:        clusterID1,
				Namespace:        test.LocalNamespace,
				GlobalnetEnabled: false,
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
					Labels: map[string]string{
						"service-label1": "value1",
						"service-label2": "value2",
					},
				},
				Spec: corev1.ServiceSpec{
					ClusterIP:       ipV4ServiceIP1,
					ClusterIPs:      []string{ipV4ServiceIP1},
					IPFamilies:      []corev1.IPFamily{corev1.IPv4Protocol},
					Selector:        map[string]string{"app": "test"},
					Ports:           []corev1.ServicePort{toServicePort(port1), toServicePort(port2)},
					SessionAffinity: corev1.ServiceAffinityNone,
				},
			},
			serviceExport: &mcsv1b1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
			},
			serviceEndpointSlices: []discovery.EndpointSlice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("%s-%s1", serviceName, clusterID1),
						Labels: map[string]string{
							discovery.LabelServiceName:      serviceName,
							"kubernetes.io/cluster-service": "true",
						},
					},
					AddressType: discovery.AddressTypeIPv4,
					Endpoints: []discovery.Endpoint{
						{
							Addresses:  []string{epIP1},
							Conditions: discovery.EndpointConditions{Ready: new(true)},
							Hostname:   new(hostName),
							TargetRef: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "one",
							},
						},
						{
							Addresses:  []string{epIP2},
							Conditions: discovery.EndpointConditions{Ready: new(true)},
							NodeName:   new(nodeName),
							TargetRef: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "two",
							},
						},
						{
							Addresses:  []string{epIP3},
							Conditions: discovery.EndpointConditions{Ready: new(false)},
							TargetRef: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "not-ready",
							},
						},
					},
					Ports: []discovery.EndpointPort{
						{
							Name:     new(port1.Name),
							Protocol: &port1.Protocol,
							Port:     &port1.Port,
						},
						{
							Name:     new(port2.Name),
							Protocol: &port2.Protocol,
							Port:     &port2.Port,
						},
					},
				},
			},
		},
		cluster2: cluster{
			clusterID:                  clusterID2,
			supportedIPFamilies:        nil,
			verifyImportReadyCondition: assertImportReady,
			agentSpec: controller.AgentSpecification{
				ClusterID:        clusterID2,
				Namespace:        test.LocalNamespace,
				GlobalnetEnabled: false,
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
				Spec: corev1.ServiceSpec{
					ClusterIP:       ipV4ServiceIP2,
					ClusterIPs:      []string{ipV4ServiceIP2},
					IPFamilies:      []corev1.IPFamily{corev1.IPv4Protocol},
					Selector:        map[string]string{"app": "test"},
					Ports:           []corev1.ServicePort{toServicePort(port1), toServicePort(port2)},
					SessionAffinity: corev1.ServiceAffinityNone,
				},
			},
			serviceExport: &mcsv1b1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
			},
			serviceEndpointSlices: []discovery.EndpointSlice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   fmt.Sprintf("%s-%s1", serviceName, clusterID2),
						Labels: map[string]string{discovery.LabelServiceName: serviceName},
					},
					AddressType: discovery.AddressTypeIPv4,
					Endpoints: []discovery.Endpoint{
						{
							Addresses:  []string{"192.168.5.3"},
							Conditions: discovery.EndpointConditions{Ready: new(true)},
							Hostname:   &hostName,
						},
					},
				},
			},
		},
		syncerConfig: &broker.SyncerConfig{
			BrokerNamespace: test.RemoteNamespace,
			RestMapper: test.GetRESTMapperFor(&mcsv1b1.ServiceExport{}, &mcsv1b1.ServiceImport{}, &corev1.Service{},
				&corev1.Endpoints{}, &corev1.Namespace{}, &discovery.EndpointSlice{}, controller.GetGlobalIngressIPObj()),
			BrokerClient: brokerClient,
			Scheme:       syncerScheme,
		},
		stopCh:  make(chan struct{}),
		doStart: true,
	}

	var err error

	t.ipPool, err = ipam.NewIPPool("243.10.1.0/24", nil)
	Expect(err).To(Succeed())

	t.brokerServiceImportReactor = fake.NewFailingReactorForResource(&brokerClient.Fake, mcsv1b1.ServiceImportPluralName)
	t.brokerEndpointSliceReactor = fake.NewFailingReactorForResource(&brokerClient.Fake, "endpointslices")

	t.cluster1.headlessEndpointAddresses = [][]discovery.Endpoint{t.cluster1.serviceEndpointSlices[0].Endpoints}

	t.cluster2.headlessEndpointAddresses = [][]discovery.Endpoint{t.cluster2.serviceEndpointSlices[0].Endpoints}

	t.brokerServiceImportClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&mcsv1b1.ServiceImport{}))

	t.brokerEndpointSliceClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace)

	t.cluster1.init(ctx, t.syncerConfig, nil, nil)
	t.cluster2.init(ctx, t.syncerConfig, nil, nil)

	return t
}

func (t *testDriver) justBeforeEach(ctx context.Context) {
	t.cluster1.start(ctx, t, *t.syncerConfig)
	t.cluster2.start(ctx, t, *t.syncerConfig)

	if t.aggregatedIPFamilies == nil {
		t.aggregatedIPFamilies = t.cluster1.service.Spec.IPFamilies
	}
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (c *cluster) init(ctx context.Context, syncerConfig *broker.SyncerConfig, dynClient dynamic.Interface, dynClientFake *k8stesting.Fake,
) {
	c.expectedClusterIPEndpoints = nil

	if dynClient == nil {
		fakeDynClient := dynamicfake.NewSimpleDynamicClient(syncerConfig.Scheme)
		c.localDynClient = fakeDynClient
		c.localDynClientFake = &fakeDynClient.Fake
		fake.AddBasicReactors(c.localDynClientFake)
	} else {
		c.localDynClient = dynClient
		c.localDynClientFake = dynClientFake
	}

	c.localServiceImportReactor = fake.NewFailingReactorForResource(c.localDynClientFake, mcsv1b1.ServiceImportPluralName)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1b1.ServiceImport{}))

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(serviceNamespace)

	c.localIngressIPClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		controller.GetGlobalIngressIPObj())).Namespace(serviceNamespace)

	// Add a K8s EPS for some other service to ensure it doesn't interfere with anything.
	_, err := endpointSliceClientFor(c.localDynClient, c.service.Namespace).Create(ctx,
		resource.MustToUnstructured(&discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "some-other-service-eps",
				Labels: map[string]string{discovery.LabelServiceName: "some-other-service"},
			},
		}), metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func (c *cluster) start(ctx context.Context, t *testDriver, syncerConfig broker.SyncerConfig) {
	for i := range c.serviceEndpointSlices {
		maps.Copy(c.serviceEndpointSlices[i].Labels, c.service.Labels)
	}

	for _, ip := range c.service.Spec.ClusterIPs {
		c.expectedClusterIPEndpoints = append(c.expectedClusterIPEndpoints, discovery.Endpoint{
			Addresses:  []string{ip},
			Conditions: discovery.EndpointConditions{Ready: new(true)},
		})
	}

	syncerConfig.LocalClient = c.localDynClient
	bigint, err := rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceImportCounterName := "submariner_service_import" + bigint.String()

	bigint, err = rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceExportCounterName := "submariner_service_export" + bigint.String()

	if c.supportedIPFamilies == nil {
		c.supportedIPFamilies = c.service.Spec.IPFamilies
	}

	c.agentController, err = controller.New(ctx, &c.agentSpec, syncerConfig,
		controller.AgentConfig{
			ServiceImportCounterName: serviceImportCounterName,
			ServiceExportCounterName: serviceExportCounterName,
			IPPool:                   t.ipPool,
			SupportedIPFamilies:      c.supportedIPFamilies,
		})

	Expect(err).To(Succeed())

	if t.doStart {
		Expect(c.agentController.Start(ctx, t.stopCh)).To(Succeed())
	}
}

func (c *cluster) createService(ctx context.Context) {
	test.CreateResource(ctx, c.dynamicServiceClientFor().Namespace(c.service.Namespace), c.service)
}

func (c *cluster) updateService(ctx context.Context) {
	test.UpdateResource(ctx, c.dynamicServiceClientFor().Namespace(c.service.Namespace), c.service)
}

func (c *cluster) deleteService(ctx context.Context) {
	Expect(c.dynamicServiceClientFor().Namespace(c.service.Namespace).Delete(ctx, c.service.Name,
		metav1.DeleteOptions{})).To(Succeed())
}

func (c *cluster) createServiceExport(ctx context.Context) {
	test.CreateResource(ctx, c.localServiceExportClient(), c.serviceExport)
}

func (c *cluster) deleteServiceExport(ctx context.Context) {
	Expect(c.localServiceExportClient().Delete(ctx, c.serviceExport.GetName(), metav1.DeleteOptions{})).To(Succeed())
}

func (c *cluster) createServiceEndpointSlices(ctx context.Context) {
	client := endpointSliceClientFor(c.localDynClient, c.service.Namespace)

	for i := range c.serviceEndpointSlices {
		_, err := client.Create(ctx, resource.MustToUnstructured(&c.serviceEndpointSlices[i]), metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			continue
		}

		Expect(err).To(Succeed())
	}
}

func (c *cluster) updateServiceEndpointSlices(ctx context.Context) {
	client := endpointSliceClientFor(c.localDynClient, c.service.Namespace)

	for i := range c.serviceEndpointSlices {
		test.UpdateResource(ctx, client, &c.serviceEndpointSlices[i])
	}
}

func (c *cluster) deleteEndpointSlice(ctx context.Context, name string) {
	Expect(endpointSliceClientFor(c.localDynClient, c.service.Namespace).Delete(ctx, name,
		metav1.DeleteOptions{})).To(Succeed())
}

func (c *cluster) createGlobalIngressIP(ctx context.Context, ingressIP *unstructured.Unstructured) {
	test.CreateResource(ctx, c.localIngressIPClient, ingressIP)
}

func (c *cluster) newHeadlessGlobalIngressIPForPod(target, ip string) *unstructured.Unstructured {
	ingressIP := c.newGlobalIngressIP("pod"+"-"+target, ip)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.HeadlessServicePod, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, target, "spec", "podRef", "name")).To(Succeed())

	return ingressIP
}

func (c *cluster) newHeadlessGlobalIngressIPForEndpointIP(name, ip, endpointIP string) *unstructured.Unstructured {
	ingressIP := c.newGlobalIngressIP("ep"+"-"+name+"-"+endpointIP, ip)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.HeadlessServiceEndpoints, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, name, "spec", "serviceRef", "name")).To(Succeed())

	annotations := map[string]string{"submariner.io/headless-svc-endpoints-ip": endpointIP}
	ingressIP.SetAnnotations(annotations)

	return ingressIP
}

func (c *cluster) newGlobalIngressIP(name, ip string) *unstructured.Unstructured {
	ingressIP := controller.GetGlobalIngressIPObj()
	ingressIP.SetName(name)
	ingressIP.SetNamespace(c.service.Namespace)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.ClusterIPService, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, c.service.Name, "spec", "serviceRef", "name")).To(Succeed())

	setIngressAllocatedIP(ingressIP, ip)
	setIngressIPConditions(ingressIP, metav1.Condition{
		Type:    "Allocated",
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: "Allocated global IP",
	})

	return ingressIP
}

func (c *cluster) retrieveServiceExportCondition(ctx context.Context, se *mcsv1b1.ServiceExport,
	condType mcsv1b1.ServiceExportConditionType,
) *metav1.Condition {
	obj, err := serviceExportClientFor(c.localDynClient, se.Namespace).Get(ctx, se.Name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return meta.FindStatusCondition(toServiceExport(obj).Status.Conditions, string(condType))
}

func (c *cluster) awaitServiceExportCondition(ctx context.Context, expected ...metav1.Condition) {
	conditionsEqual := func(actual, expected *metav1.Condition) bool {
		return actual != nil && actual.Type == expected.Type && actual.Status == expected.Status &&
			actual.Reason == expected.Reason
	}

	lastIndex := -1

	for i := range len(expected) - 1 {
		j := lastIndex + 1

		Eventually(ctx, func(g Gomega) {
			var (
				found *metav1.Condition
				all   []*metav1.Condition
			)

			actions := c.localDynClientFake.Actions()
			for j < len(actions) {
				a := actions[j]
				j++

				if !a.Matches("update", mcsv1b1.ServiceExportPluralName) {
					continue
				}

				found = meta.FindStatusCondition(toServiceExport(a.(k8stesting.UpdateActionImpl).Object).Status.Conditions,
					expected[i].Type)

				if found != nil {
					all = append(all, found)
				}

				if conditionsEqual(found, &expected[i]) {
					lastIndex = j
					break
				}
			}

			g.Expect(found).NotTo(BeNil(), "ServiceExport condition not received. Expected: %s\nActual: %s",
				resource.ToJSON(expected[i]), resource.ToJSON(all))
			assertEquivalentConditions(g, found, &expected[i])
		}).Should(Succeed())
	}

	last := len(expected) - 1

	Eventually(ctx, func(g Gomega, ctx context.Context) {
		obj, err := c.localServiceExportClient().Get(ctx, c.serviceExport.Name, metav1.GetOptions{})
		Expect(err).To(Succeed())

		se := toServiceExport(obj)
		c := meta.FindStatusCondition(se.Status.Conditions, expected[last].Type)

		g.Expect(c).NotTo(BeNil(), "ServiceExport condition not found for type %q", expected[last].Type)
		assertEquivalentConditions(g, c, &expected[last])
	}).Within(time.Second * 3).Should(Succeed())
}

//nolint:gocritic // Ignore hugeParam
func (c *cluster) ensureLastServiceExportCondition(ctx context.Context, expected metav1.Condition) {
	indexOfLastCondition := func() int {
		actions := c.localDynClientFake.Actions()
		for i := len(actions) - 1; i >= 0; i-- {
			if !actions[i].Matches("update", mcsv1b1.ServiceExportPluralName) {
				continue
			}

			actual := meta.FindStatusCondition(
				toServiceExport(actions[i].(k8stesting.UpdateActionImpl).Object).Status.Conditions, expected.Type)

			if actual != nil {
				assertEquivalentConditions(Default, actual, &expected)
				return i
			}
		}

		Fail("ServiceExport condition not found. Expected: " + resource.ToJSON(expected))

		return -1
	}

	initialIndex := indexOfLastCondition()
	Consistently(ctx, func() int {
		return indexOfLastCondition()
	}).Should(Equal(initialIndex), "Expected ServiceExport condition to not change: "+resource.ToJSON(expected))
}

func (c *cluster) ensureNoServiceExportCondition(ctx context.Context, condType mcsv1b1.ServiceExportConditionType,
	serviceExports ...*mcsv1b1.ServiceExport,
) {
	if len(serviceExports) == 0 {
		serviceExports = []*mcsv1b1.ServiceExport{c.serviceExport}
	}

	for _, se := range serviceExports {
		Consistently(ctx, func() any {
			return c.retrieveServiceExportCondition(ctx, se, condType)
		}).Should(BeNil(), "Unexpected ServiceExport status condition")
	}
}

func (c *cluster) awaitNoServiceStatus(ctx context.Context) {
	c.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionFalse, mcsv1b1.ServiceExportReasonNoService))
}

func (c *cluster) findLocalServiceImport(ctx context.Context) *mcsv1b1.ServiceImport {
	list, err := c.localServiceImportClient.Namespace(test.LocalNamespace).List(ctx, metav1.ListOptions{})
	Expect(err).To(Succeed())

	for i := range list.Items {
		if list.Items[i].GetLabels()[mcsv1b1.LabelServiceName] == c.service.Name &&
			list.Items[i].GetLabels()[constants.LabelSourceNamespace] == c.service.Namespace {
			serviceImport := toServiceImport(&list.Items[i])

			return serviceImport
		}
	}

	return nil
}

func (c *cluster) findLocalEndpointSlices(ctx context.Context) []*discovery.EndpointSlice {
	return findEndpointSlices(ctx, c.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
}

func (c *cluster) ensureNoEndpointSlice(ctx context.Context) {
	Consistently(ctx, func(ctx context.Context) int {
		return len(findEndpointSlices(ctx, c.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID))
	}, 300*time.Millisecond).Should(BeZero(), "Unexpected EndpointSlice")
}

func (c *cluster) ensureNoServiceExportActions() {
	c.localDynClientFake.ClearActions()

	Consistently(func() []string {
		return testutil.GetOccurredActionVerbs(c.localDynClientFake, mcsv1b1.ServiceExportPluralName, "get", "update")
	}, 500*time.Millisecond).Should(BeEmpty())
}

func (c *cluster) verifyServiceImportReady(si *mcsv1b1.ServiceImport) {
	cond := meta.FindStatusCondition(si.Status.Conditions, string(mcsv1b1.ServiceImportConditionReady))
	Expect(cond).ToNot(BeNil(), "ServiceImport Ready condition not found")
	c.verifyImportReadyCondition(cond)
}

func (c *cluster) localServiceExportClient() dynamic.ResourceInterface {
	return serviceExportClientFor(c.localDynClient, c.serviceExport.Namespace)
}

func awaitServiceImport(ctx context.Context, client dynamic.NamespaceableResourceInterface, expected *mcsv1b1.ServiceImport,
	ipPool *ipam.IPPool,
) *mcsv1b1.ServiceImport {
	sortSlices := func(si *mcsv1b1.ServiceImport) {
		sort.SliceStable(si.Spec.Ports, func(i, j int) bool {
			return si.Spec.Ports[i].Port < si.Spec.Ports[j].Port
		})

		sort.SliceStable(si.Spec.IPFamilies, func(i, j int) bool {
			return si.Spec.IPFamilies[i] < si.Spec.IPFamilies[j]
		})

		sort.SliceStable(si.Status.Clusters, func(i, j int) bool {
			return si.Status.Clusters[i].Cluster < si.Status.Clusters[j].Cluster
		})
	}

	sortSlices(expected)

	var serviceImport *mcsv1b1.ServiceImport

	Eventually(ctx, func(g Gomega, ctx context.Context) {
		obj, err := client.Namespace(expected.Namespace).Get(ctx, expected.Name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		serviceImport = toServiceImport(obj)
		sortSlices(serviceImport)

		g.Expect(serviceImport.Spec.IPs).To(HaveLen(len(expected.Spec.IPs)))

		g.Expect(serviceImport.Spec).To(BeComparableTo(expected.Spec,
			cmpopts.IgnoreFields(mcsv1b1.ServiceImportSpec{}, "IPs")),
			"Actual Spec: %s, Expected Spec %s", resource.ToJSON(serviceImport.Spec), resource.ToJSON(expected.Spec))
		g.Expect(serviceImport.Status).To(BeComparableTo(expected.Status,
			cmpopts.IgnoreFields(mcsv1b1.ServiceImportStatus{}, "Conditions")),
			"Actual Status: %s, Expected Status %s", resource.ToJSON(serviceImport.Status), resource.ToJSON(expected.Status))
	}).Within(5 * time.Second).ProbeEvery(50 * time.Millisecond).Should(Succeed())

	if len(serviceImport.Spec.IPs) > 0 {
		Expect(ipPool.Reserve(serviceImport.Spec.IPs...)).ToNot(Succeed(), "ServiceImport IP was not allocated or reserved")
	}

	Expect(serviceImport.Labels).To(BeEmpty())

	return serviceImport
}

func getServiceImport(ctx context.Context, client dynamic.NamespaceableResourceInterface, namespace, name string) *mcsv1b1.ServiceImport {
	obj, err := client.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return toServiceImport(obj)
}

func findEndpointSlices(ctx context.Context, client dynamic.ResourceInterface, namespace, name, clusterID string,
) []*discovery.EndpointSlice {
	list, err := client.List(ctx, metav1.ListOptions{})
	if resource.IsMissingNamespaceErr(err) {
		return []*discovery.EndpointSlice{}
	}

	Expect(err).To(Succeed())

	var endpointSlices []*discovery.EndpointSlice

	for i := range list.Items {
		labels := list.Items[i].GetLabels()
		nameMatch := name == "" || labels[mcsv1b1.LabelServiceName] == name || labels[discovery.LabelServiceName] == name
		namespaceMatch := namespace == "" || labels[constants.LabelSourceNamespace] == namespace
		clusterMatch := clusterID == "" || labels[mcsv1b1.LabelSourceCluster] == clusterID

		if nameMatch && namespaceMatch && clusterMatch {
			eps := &discovery.EndpointSlice{}
			Expect(scheme.Scheme.Convert(&list.Items[i], eps, nil)).To(Succeed())

			endpointSlices = append(endpointSlices, eps)
		}
	}

	return endpointSlices
}

func awaitEndpointSlice(ctx context.Context, client dynamic.ResourceInterface, serviceName string, expected *discovery.EndpointSlice) {
	sortSlices := func(eps *discovery.EndpointSlice) {
		sort.SliceStable(eps.Ports, func(i, j int) bool {
			return *eps.Ports[i].Port < *eps.Ports[j].Port
		})

		sort.SliceStable(eps.Endpoints, func(i, j int) bool {
			return eps.Endpoints[i].Addresses[0] < eps.Endpoints[j].Addresses[0]
		})
	}

	sortSlices(expected)

	Eventually(ctx, func(g Gomega, ctx context.Context) {
		var endpointSlice *discovery.EndpointSlice

		slices := findEndpointSlices(ctx, client, expected.Namespace, serviceName, expected.Labels[mcsv1b1.LabelSourceCluster])

		for _, eps := range slices {
			if expected.Labels[constants.LabelIsHeadless] == strconv.FormatBool(true) {
				if eps.Labels[constants.LabelSourceName] == expected.Name {
					endpointSlice = eps
					break
				}
			} else if eps.AddressType == expected.AddressType {
				endpointSlice = eps
				break
			}
		}

		g.Expect(endpointSlice).NotTo(BeNil(), "EndpointSlice not found: %s", resource.ToJSON(expected))

		sortSlices(endpointSlice)

		maps.DeleteFunc(endpointSlice.Labels, func(k, _ string) bool {
			return strings.HasPrefix(k, "submariner-io/")
		})

		g.Expect(endpointSlice.Labels).To(Equal(expected.Labels), "%s EndpointSlice", expected.AddressType)

		for k, v := range expected.Annotations {
			g.Expect(endpointSlice.Annotations).To(HaveKeyWithValue(k, v), "%s EndpointSlice", expected.AddressType)
		}

		g.Expect(endpointSlice.Endpoints).To(Equal(expected.Endpoints), "%s EndpointSlice", expected.AddressType)
		g.Expect(endpointSlice.Ports).To(Equal(expected.Ports), "%s EndpointSlice", expected.AddressType)
	}).ProbeEvery(time.Millisecond * 50).Within(time.Second * 5).Should(Succeed())
}

func awaitNoEndpointSlice(ctx context.Context, client dynamic.ResourceInterface, ns, name, clusterID string) {
	Eventually(ctx, func(g Gomega, ctx context.Context) {
		eps := findEndpointSlices(ctx, client, ns, name, clusterID)
		g.Expect(eps).To(BeEmpty(), "Unexpected EndpointSlice found")
	}).Should(Succeed())
}

func (c *cluster) dynamicServiceClientFor() dynamic.NamespaceableResourceInterface {
	return c.localDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"})
}

func (t *testDriver) awaitAggregatedServiceImport(ctx context.Context, sType mcsv1b1.ServiceImportType, name, ns string,
	clusters ...*cluster,
) {
	expServiceImport := &mcsv1b1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", name, ns),
			Namespace: test.RemoteNamespace,
		},
		Spec: mcsv1b1.ServiceImportSpec{
			Type:                  sType,
			Ports:                 []mcsv1b1.ServicePort{},
			IPFamilies:            t.aggregatedIPFamilies,
			SessionAffinity:       t.aggregatedSessionAffinity,
			SessionAffinityConfig: t.aggregatedSessionAffinityConfig,
			TrafficDistribution:   t.aggregatedTrafficDistribution,
			InternalTrafficPolicy: t.aggregatedInternalTrafficPolicy,
		},
	}

	if len(clusters) > 0 {
		if sType == mcsv1b1.ClusterSetIP {
			expServiceImport.Spec.Ports = t.aggregatedServicePorts

			if t.useClusterSetIP {
				expServiceImport.Spec.IPs = []string{"1.1.1.1"}
			}
		}

		for _, c := range clusters {
			expServiceImport.Status.Clusters = append(expServiceImport.Status.Clusters,
				mcsv1b1.ClusterStatus{Cluster: c.clusterID})
		}
	}

	actual := awaitServiceImport(ctx, t.brokerServiceImportClient, expServiceImport, t.ipPool)

	if sType == mcsv1b1.ClusterSetIP {
		Expect(actual.Annotations).To(HaveKeyWithValue(constants.UseClustersetIP, strconv.FormatBool(t.useClusterSetIP)))
	}

	expServiceImport.Name = name
	expServiceImport.Namespace = ns

	t.cluster1.verifyServiceImportReady(awaitServiceImport(ctx, t.cluster1.localServiceImportClient, expServiceImport, t.ipPool))
	t.cluster2.verifyServiceImportReady(awaitServiceImport(ctx, t.cluster2.localServiceImportClient, expServiceImport, t.ipPool))
}

func (t *testDriver) ensureAggregatedServiceImport(ctx context.Context, sType mcsv1b1.ServiceImportType, name, ns string,
	clusters ...*cluster,
) {
	Consistently(ctx, func(ctx context.Context) bool {
		t.awaitAggregatedServiceImport(ctx, sType, name, ns, clusters...)
		return true
	}).Should(BeTrue())
}

func (t *testDriver) awaitNoAggregatedServiceImport(ctx context.Context, c *cluster) {
	test.AwaitNoResource(ctx, t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
		fmt.Sprintf("%s-%s", c.service.Name, c.service.Namespace))
	test.AwaitNoResource(ctx, t.cluster1.localServiceImportClient.Namespace(c.service.Namespace), c.service.Name)
	test.AwaitNoResource(ctx, t.cluster2.localServiceImportClient.Namespace(c.service.Namespace), c.service.Name)
}

func (t *testDriver) awaitEndpointSlice(ctx context.Context, c *cluster) {
	isHeadless := c.service.Spec.ClusterIP == corev1.ClusterIPNone

	epsTemplate := &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: c.service.Namespace,
			Labels: map[string]string{
				discovery.LabelManagedBy:       constants.LabelValueManagedBy,
				mcsv1b1.LabelSourceCluster:     c.clusterID,
				mcsv1b1.LabelServiceName:       c.service.Name,
				constants.LabelSourceNamespace: c.service.Namespace,
				constants.LabelIsHeadless:      strconv.FormatBool(isHeadless),
			},
			Annotations: map[string]string{},
		},
		AddressType: discovery.AddressTypeIPv4,
	}

	maps.Copy(epsTemplate.Labels, c.service.Labels)

	var expected []discovery.EndpointSlice

	if isHeadless {
		epsTemplate.Annotations[constants.PublishNotReadyAddresses] = strconv.FormatBool(c.service.Spec.PublishNotReadyAddresses)
		epsTemplate.Annotations[constants.GlobalnetEnabled] = strconv.FormatBool(c.agentSpec.GlobalnetEnabled)

		for i := range c.headlessEndpointAddresses {
			eps := epsTemplate.DeepCopy()
			eps.Endpoints = c.headlessEndpointAddresses[i]
			eps.Ports = c.serviceEndpointSlices[i].Ports
			eps.Name = c.serviceEndpointSlices[i].Name
			eps.Labels[constants.LabelSourceName] = c.serviceEndpointSlices[i].Name
			expected = append(expected, *eps)
		}
	} else {
		for i := range c.expectedClusterIPEndpoints {
			eps := epsTemplate.DeepCopy()

			eps.Endpoints = []discovery.Endpoint{c.expectedClusterIPEndpoints[i]}

			if k8snet.IPFamilyOfString(c.expectedClusterIPEndpoints[i].Addresses[0]) == k8snet.IPv6 {
				eps.AddressType = discovery.AddressTypeIPv6
			}

			for i := range c.service.Spec.Ports {
				eps.Ports = append(eps.Ports, discovery.EndpointPort{
					Name:        &c.service.Spec.Ports[i].Name,
					Protocol:    &c.service.Spec.Ports[i].Protocol,
					Port:        &c.service.Spec.Ports[i].Port,
					AppProtocol: c.service.Spec.Ports[i].AppProtocol,
				})
			}

			expected = append(expected, *eps)
		}
	}

	for i := range expected {
		awaitEndpointSlice(ctx, t.brokerEndpointSliceClient, c.service.Name, &expected[i])
		awaitEndpointSlice(ctx, t.cluster1.localEndpointSliceClient, c.service.Name, &expected[i])
		awaitEndpointSlice(ctx, t.cluster2.localEndpointSliceClient, c.service.Name, &expected[i])
	}

	Eventually(ctx, func(ctx context.Context) []*discovery.EndpointSlice {
		return findEndpointSlices(ctx, c.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
	}).Should(HaveLen(len(expected)))
}

func (t *testDriver) ensureEndpointSlice(ctx context.Context, c *cluster) {
	Consistently(ctx, func(ctx context.Context) bool {
		t.awaitEndpointSlice(ctx, c)
		return true
	}).Should(BeTrue())
}

func (t *testDriver) awaitNoEndpointSlice(ctx context.Context, c *cluster) {
	awaitNoEndpointSlice(ctx, t.cluster1.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
	awaitNoEndpointSlice(ctx, t.brokerEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
	awaitNoEndpointSlice(ctx, t.cluster2.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
}

func serviceExportClientFor(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(schema.GroupVersionResource{
		Group:    mcsv1b1.GroupVersion.Group,
		Version:  mcsv1b1.GroupVersion.Version,
		Resource: mcsv1b1.ServiceExportPluralName,
	}).Namespace(namespace)
}

func endpointSliceClientFor(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(discovery.SchemeGroupVersion.WithResource("endpointslices")).Namespace(namespace)
}

func assertEquivalentConditions(g Gomega, actual, expected *metav1.Condition) {
	out := resource.ToJSON(actual)

	g.Expect(actual.Status).To(Equal(expected.Status), "Condition Status differs. Actual: %s", out)
	g.Expect(actual.LastTransitionTime).To(Not(BeNil()), "Condition LastTransitionTime. Actual: %s", out)
	Expect(actual.Reason).NotTo(BeEmpty(), "Condition Reason cannot be empty. Actual: %s", out)
	g.Expect(actual.Reason).To(Equal(expected.Reason), "Condition Reason differs. Actual: %s", out)

	if expected.Message != "" {
		g.Expect(actual.Message).To(ContainSubstring(expected.Message), "Condition Message differs. Actual: %s", out)
	}
}

func toServiceExport(obj any) *mcsv1b1.ServiceExport {
	se := &mcsv1b1.ServiceExport{}
	Expect(scheme.Scheme.Convert(obj, se, nil)).To(Succeed())

	return se
}

func toServiceImport(obj any) *mcsv1b1.ServiceImport {
	si := &mcsv1b1.ServiceImport{}
	Expect(scheme.Scheme.Convert(obj, si, nil)).To(Succeed())

	return si
}

func (t *testDriver) awaitNonHeadlessServiceExported(ctx context.Context, clusters ...*cluster) {
	t.awaitServiceExported(ctx, mcsv1b1.ClusterSetIP, clusters...)
}

func (t *testDriver) awaitHeadlessServiceExported(ctx context.Context, clusters ...*cluster) {
	t.awaitServiceExported(ctx, mcsv1b1.Headless, clusters...)
}

func (t *testDriver) awaitServiceExported(ctx context.Context, sType mcsv1b1.ServiceImportType, clusters ...*cluster) {
	t.awaitAggregatedServiceImport(ctx, sType, t.cluster1.service.Name, t.cluster1.service.Namespace, clusters...)

	for _, c := range clusters {
		Eventually(ctx, func(g Gomega, ctx context.Context) {
			list, err := t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: k8slabels.Set(map[string]string{
					mcsv1b1.LabelServiceName:       t.cluster1.service.Name,
					constants.LabelSourceNamespace: t.cluster1.service.Namespace,
					mcsv1b1.LabelSourceCluster:     c.clusterID,
				}).String(),
			})
			Expect(err).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1), "Local ServiceImport for %q on the broker", c.clusterID)
		}).Should(Succeed())

		t.awaitEndpointSlice(ctx, c)

		c.awaitServiceExportCondition(ctx, newServiceExportValidCondition(metav1.ConditionTrue, mcsv1b1.ServiceExportReasonValid))
		c.awaitServiceExportCondition(ctx, newServiceExportReadyCondition(metav1.ConditionTrue, mcsv1b1.ServiceExportReasonExported))
	}
}

func (t *testDriver) awaitServiceUnexported(ctx context.Context, c *cluster) {
	t.awaitNoEndpointSlice(ctx, c)

	t.awaitNoAggregatedServiceImport(ctx, c)

	Eventually(ctx, func(g Gomega, ctx context.Context) {
		list, err := t.brokerServiceImportClient.Namespace(test.RemoteNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: k8slabels.SelectorFromSet(k8slabels.Set(map[string]string{
				mcsv1b1.LabelServiceName:       c.service.Name,
				constants.LabelSourceNamespace: c.service.Namespace,
				mcsv1b1.LabelSourceCluster:     c.clusterID,
			})).String(),
		})

		Expect(err).NotTo(HaveOccurred())
		g.Expect(list.Items).To(BeEmpty(), "Found unexpected ServiceImport on the broker")
	}).Within(time.Second * 3).Should(Succeed())

	c.localDynClientFake.ClearActions()

	// Ensure the service's EndpointSlices are no longer being watched by creating a EndpointSlice and verifying the
	// exported EndpointSlice isn't recreated.
	epsClient := endpointSliceClientFor(c.localDynClient, c.service.Namespace)

	_, err := epsClient.Create(ctx,
		resource.MustToUnstructured(&discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "dummy",
				Labels: map[string]string{discovery.LabelServiceName: serviceName},
			},
		}), metav1.CreateOptions{})
	Expect(err).To(Succeed())

	c.ensureNoEndpointSlice(ctx)
	Expect(epsClient.Delete(ctx, "dummy", metav1.DeleteOptions{})).To(Succeed())
}

func newServiceExportValidCondition(status metav1.ConditionStatus, reason mcsv1b1.ServiceExportConditionReason) metav1.Condition {
	return mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionValid, status, reason, "")
}

func newServiceExportReadyCondition(status metav1.ConditionStatus, reason mcsv1b1.ServiceExportConditionReason) metav1.Condition {
	return mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionReady, status, reason, "")
}

func newServiceExportConflictCondition(reason ...mcsv1b1.ServiceExportConditionReason) metav1.Condition {
	var joined strings.Builder

	for i := range reason {
		if i > 0 {
			joined.WriteString(",")
		}

		joined.WriteString(string(reason[i]))
	}

	return mcsv1b1.NewServiceExportCondition(mcsv1b1.ServiceExportConditionConflict, metav1.ConditionTrue,
		mcsv1b1.ServiceExportConditionReason(joined.String()), "")
}

func setIngressIPConditions(ingressIP *unstructured.Unstructured, conditions ...metav1.Condition) {
	var err error

	condObjs := make([]any, len(conditions))
	for i := range conditions {
		condObjs[i], err = runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		Expect(err).To(Succeed())
	}

	Expect(unstructured.SetNestedSlice(ingressIP.Object, condObjs, "status", "conditions")).To(Succeed())
}

func setIngressAllocatedIP(ingressIP *unstructured.Unstructured, ip string) {
	Expect(unstructured.SetNestedField(ingressIP.Object, ip, "status", "allocatedIP")).To(Succeed())
}

func toServicePort(port mcsv1b1.ServicePort) corev1.ServicePort {
	return corev1.ServicePort{
		Name:        port.Name,
		Protocol:    port.Protocol,
		Port:        port.Port,
		AppProtocol: port.AppProtocol,
	}
}

func assertImportReady(c *metav1.Condition) {
	Expect(c.Status).To(Equal(metav1.ConditionTrue))
	Expect(c.Reason).To(Equal(string(mcsv1b1.ServiceImportReasonReady)))
}

func assertImportNotReady(c *metav1.Condition) {
	Expect(c.Status).To(Equal(metav1.ConditionFalse))
	Expect(c.Reason).To(Equal(string(mcsv1b1.ServiceImportReasonIPFamilyNotSupported)))
}
