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
	"errors"
	"flag"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/pointer"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	clusterID1       = "east"
	clusterID2       = "west"
	serviceName      = "nginx"
	serviceNamespace = "service-ns"
	globalIP1        = "242.254.1.1"
	globalIP2        = "242.254.1.2"
)

var (
	nodeName = "my-node"
	hostName = "my-host"

	port1 = mcsv1a1.ServicePort{
		Name:     "http",
		Protocol: corev1.ProtocolTCP,
		Port:     8080,
	}

	port2 = mcsv1a1.ServicePort{
		Name:     "https",
		Protocol: corev1.ProtocolTCP,
		Port:     8443,
	}

	port3 = mcsv1a1.ServicePort{
		Name:        "POP3",
		Protocol:    corev1.ProtocolUDP,
		Port:        110,
		AppProtocol: pointer.String("smtp"),
	}
)

func init() {
	// set logging verbosity of agent in unit test to DEBUG
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	//nolint:errcheck // Ignore errors; CommandLine is set for ExitOnError.
	flags.Parse([]string{"-v=2"})
	kzerolog.InitK8sLogging()

	err := mcsv1a1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Controller Suite")
}

type cluster struct {
	agentSpec                 controller.AgentSpecification
	localDynClient            *dynamicfake.FakeDynamicClient
	localServiceExportClient  dynamic.ResourceInterface
	localServiceImportClient  dynamic.NamespaceableResourceInterface
	localIngressIPClient      dynamic.ResourceInterface
	localEndpointSliceClient  dynamic.ResourceInterface
	localServiceImportReactor *fake.FailingReactor
	agentController           *controller.Controller
	service                   *corev1.Service
	serviceIP                 string
	serviceExport             *mcsv1a1.ServiceExport
	endpoints                 *corev1.Endpoints
	clusterID                 string
	endpointSliceAddresses    []discovery.Endpoint
}

type testDriver struct {
	cluster1                   cluster
	cluster2                   cluster
	brokerServiceImportClient  dynamic.NamespaceableResourceInterface
	brokerEndpointSliceClient  dynamic.ResourceInterface
	brokerEndpointSliceReactor *fake.FailingReactor
	stopCh                     chan struct{}
	syncerConfig               *broker.SyncerConfig
	doStart                    bool
	brokerServiceImportReactor *fake.FailingReactor
	aggregatedServicePorts     []mcsv1a1.ServicePort
}

func newTestDiver() *testDriver {
	syncerScheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(syncerScheme)).To(Succeed())
	Expect(discovery.AddToScheme(syncerScheme)).To(Succeed())
	Expect(mcsv1a1.AddToScheme(syncerScheme)).To(Succeed())

	syncerScheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "submariner.io",
		Version: "v1",
		Kind:    "GlobalIngressIPList",
	}, &unstructured.UnstructuredList{})

	brokerClient := dynamicfake.NewSimpleDynamicClient(syncerScheme)
	fake.AddFilteringListReactor(&brokerClient.Fake)

	t := &testDriver{
		aggregatedServicePorts: []mcsv1a1.ServicePort{port1, port2},
		cluster1: cluster{
			clusterID: clusterID1,
			agentSpec: controller.AgentSpecification{
				ClusterID:        clusterID1,
				Namespace:        test.LocalNamespace,
				GlobalnetEnabled: false,
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "10.253.9.1",
					Selector:  map[string]string{"app": "test"},
					Ports:     []corev1.ServicePort{toServicePort(port1), toServicePort(port2)},
				},
			},
			serviceExport: &mcsv1a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
			},
			endpoints: &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
					Labels:    map[string]string{"app": "test"},
				},
				Subsets: []corev1.EndpointSubset{
					{
						Ports: []corev1.EndpointPort{
							{
								Name:     port1.Name,
								Protocol: port1.Protocol,
								Port:     80,
							},
							{
								Name:     port2.Name,
								Protocol: port2.Protocol,
								Port:     443,
							},
						},
						Addresses: []corev1.EndpointAddress{
							{
								IP:       "192.168.5.1",
								Hostname: hostName,
								TargetRef: &corev1.ObjectReference{
									Name: "one",
								},
							},
							{
								IP:       "192.168.5.2",
								NodeName: &nodeName,
								TargetRef: &corev1.ObjectReference{
									Name: "two",
								},
							},
						},
						NotReadyAddresses: []corev1.EndpointAddress{
							{
								IP: "10.253.6.1",
								TargetRef: &corev1.ObjectReference{
									Name: "not-ready",
								},
							},
						},
					},
				},
			},
		},
		cluster2: cluster{
			clusterID: clusterID2,
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
					ClusterIP: "10.253.10.1",
					Selector:  map[string]string{"app": "test"},
					Ports:     []corev1.ServicePort{toServicePort(port1), toServicePort(port2)},
				},
			},
			serviceExport: &mcsv1a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
				},
			},
			endpoints: &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: serviceNamespace,
					Labels:    map[string]string{"app": "test"},
				},
				Subsets: []corev1.EndpointSubset{
					{
						Addresses: []corev1.EndpointAddress{
							{
								IP:       "192.168.5.3",
								Hostname: hostName,
							},
						},
					},
				},
			},
		},
		syncerConfig: &broker.SyncerConfig{
			BrokerNamespace: test.RemoteNamespace,
			RestMapper: test.GetRESTMapperFor(&mcsv1a1.ServiceExport{}, &mcsv1a1.ServiceImport{}, &corev1.Service{},
				&corev1.Endpoints{}, &discovery.EndpointSlice{}, controller.GetGlobalIngressIPObj()),
			BrokerClient: brokerClient,
			Scheme:       syncerScheme,
		},
		stopCh:  make(chan struct{}),
		doStart: true,
	}

	t.brokerServiceImportReactor = fake.NewFailingReactorForResource(&brokerClient.Fake, "serviceimports")
	t.brokerEndpointSliceReactor = fake.NewFailingReactorForResource(&brokerClient.Fake, "endpointslices")

	t.cluster1.endpointSliceAddresses = []discovery.Endpoint{
		{
			Addresses:  []string{t.cluster1.endpoints.Subsets[0].Addresses[0].IP},
			Conditions: discovery.EndpointConditions{Ready: pointer.Bool(true)},
			Hostname:   &t.cluster1.endpoints.Subsets[0].Addresses[0].Hostname,
		},
		{
			Addresses:  []string{t.cluster1.endpoints.Subsets[0].Addresses[1].IP},
			Conditions: discovery.EndpointConditions{Ready: pointer.Bool(true)},
			Hostname:   &t.cluster1.endpoints.Subsets[0].Addresses[1].TargetRef.Name,
			NodeName:   t.cluster1.endpoints.Subsets[0].Addresses[1].NodeName,
		},
	}

	t.cluster2.endpointSliceAddresses = []discovery.Endpoint{
		{
			Addresses:  []string{t.cluster2.endpoints.Subsets[0].Addresses[0].IP},
			Conditions: discovery.EndpointConditions{Ready: pointer.Bool(true)},
			Hostname:   &t.cluster2.endpoints.Subsets[0].Addresses[0].Hostname,
		},
	}

	t.brokerServiceImportClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{}))

	t.brokerEndpointSliceClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace)

	t.cluster1.init(t.syncerConfig)
	t.cluster2.init(t.syncerConfig)

	return t
}

func (t *testDriver) justBeforeEach() {
	t.cluster1.start(t, *t.syncerConfig)
	t.cluster2.start(t, *t.syncerConfig)
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (c *cluster) init(syncerConfig *broker.SyncerConfig) {
	c.serviceIP = c.service.Spec.ClusterIP

	c.localDynClient = dynamicfake.NewSimpleDynamicClient(syncerConfig.Scheme)
	fake.AddFilteringListReactor(&c.localDynClient.Fake)
	fake.NewWatchReactor(&c.localDynClient.Fake)

	c.localServiceImportReactor = fake.NewFailingReactorForResource(&c.localDynClient.Fake, "serviceimports")

	fake.AddDeleteCollectionReactor(&c.localDynClient.Fake,
		discovery.SchemeGroupVersion.WithKind("EndpointSlice"))

	c.localServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceExport{})).Namespace(serviceNamespace)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{}))

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(serviceNamespace)

	c.localIngressIPClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		controller.GetGlobalIngressIPObj())).Namespace(serviceNamespace)
}

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func (c *cluster) start(t *testDriver, syncerConfig broker.SyncerConfig) {
	syncerConfig.LocalClient = c.localDynClient
	bigint, err := rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceImportCounterName := "submariner_service_import" + bigint.String()

	bigint, err = rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceExportCounterName := "submariner_service_export" + bigint.String()

	c.agentController, err = controller.New(&c.agentSpec, syncerConfig,
		controller.AgentConfig{
			ServiceImportCounterName: serviceImportCounterName,
			ServiceExportCounterName: serviceExportCounterName,
		})

	Expect(err).To(Succeed())

	if t.doStart {
		Expect(c.agentController.Start(t.stopCh)).To(Succeed())
	}
}

func (c *cluster) createService() {
	test.CreateResource(c.dynamicServiceClientFor().Namespace(c.service.Namespace), c.service)
}

func (c *cluster) updateService() {
	test.UpdateResource(c.dynamicServiceClientFor().Namespace(c.service.Namespace), c.service)
}

func (c *cluster) deleteService() {
	Expect(c.dynamicServiceClientFor().Namespace(c.service.Namespace).Delete(context.TODO(), c.service.Name,
		metav1.DeleteOptions{})).To(Succeed())
}

func (c *cluster) createServiceExport() {
	test.CreateResource(c.localServiceExportClient, c.serviceExport)
}

func (c *cluster) deleteServiceExport() {
	Expect(c.localServiceExportClient.Delete(context.TODO(), c.serviceExport.GetName(), metav1.DeleteOptions{})).To(Succeed())
}

func (c *cluster) createEndpoints() {
	test.CreateResource(endpointsClientFor(c.localDynClient, c.endpoints.Namespace), c.endpoints)
}

func (c *cluster) updateEndpoints() {
	test.UpdateResource(endpointsClientFor(c.localDynClient, c.endpoints.Namespace), c.endpoints)
}

func (c *cluster) createGlobalIngressIP(ingressIP *unstructured.Unstructured) {
	test.CreateResource(c.localIngressIPClient, ingressIP)
}

func (c *cluster) newHeadlessGlobalIngressIP(target, ip string) *unstructured.Unstructured {
	ingressIP := c.newGlobalIngressIP("pod"+"-"+target, ip)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.HeadlessServicePod, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, target, "spec", "podRef", "name")).To(Succeed())

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

func (c *cluster) retrieveServiceExportCondition(condType mcsv1a1.ServiceExportConditionType) *mcsv1a1.ServiceExportCondition {
	obj, err := c.localServiceExportClient.Get(context.TODO(), c.serviceExport.Name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return controller.FindServiceExportStatusCondition(toServiceExport(obj).Status.Conditions, condType)
}

func (c *cluster) awaitServiceExportCondition(expected ...*mcsv1a1.ServiceExportCondition) {
	conditionsEqual := func(actual, expected *mcsv1a1.ServiceExportCondition) bool {
		return actual != nil && actual.Type == expected.Type && actual.Status == expected.Status &&
			reflect.DeepEqual(actual.Reason, expected.Reason)
	}

	actual := make([]*mcsv1a1.ServiceExportCondition, len(expected))
	lastIndex := -1

	for i := range expected {
		expStr := resource.ToJSON(expected[i])
		j := lastIndex + 1

		Eventually(func() interface{} {
			actions := c.localDynClient.Fake.Actions()
			for j < len(actions) {
				a := actions[j]
				j++

				if !a.Matches("update", "serviceexports") {
					continue
				}

				actual[i] = controller.FindServiceExportStatusCondition(
					toServiceExport(a.(k8stesting.UpdateActionImpl).Object).Status.Conditions, expected[i].Type)

				if conditionsEqual(actual[i], expected[i]) {
					lastIndex = j

					return actual[i]
				}
			}

			return nil
		}).ShouldNot(BeNil(), fmt.Sprintf("ServiceExport condition not received. Expected: %s", expStr))
	}

	for i := range expected {
		assertEquivalentConditions(actual[i], expected[i])
	}
}

func (c *cluster) ensureLastServiceExportCondition(expected *mcsv1a1.ServiceExportCondition) {
	indexOfLastCondition := func() int {
		actions := c.localDynClient.Fake.Actions()
		for i := len(actions) - 1; i >= 0; i-- {
			if !actions[i].Matches("update", "serviceexports") {
				continue
			}

			actual := controller.FindServiceExportStatusCondition(
				toServiceExport(actions[i].(k8stesting.UpdateActionImpl).Object).Status.Conditions, expected.Type)

			if actual != nil {
				assertEquivalentConditions(actual, expected)
				return i
			}
		}

		Fail(fmt.Sprintf("ServiceExport condition not found. Expected: %s", resource.ToJSON(expected)))

		return -1
	}

	initialIndex := indexOfLastCondition()
	Consistently(func() int {
		return indexOfLastCondition()
	}).Should(Equal(initialIndex), fmt.Sprintf("Expected ServiceExport condition to not change: %s",
		resource.ToJSON(expected)))
}

func (c *cluster) ensureNoServiceExportCondition(condType mcsv1a1.ServiceExportConditionType) {
	Consistently(func() interface{} {
		return c.retrieveServiceExportCondition(condType)
	}).Should(BeNil(), "Unexpected ServiceExport status condition")
}

func (c *cluster) awaitNoServiceExportCondition(condType mcsv1a1.ServiceExportConditionType) {
	Eventually(func() interface{} {
		return c.retrieveServiceExportCondition(condType)
	}).Should(BeNil(), "Unexpected ServiceExport status condition")
}

func (c *cluster) awaitServiceUnavailableStatus() {
	c.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "ServiceUnavailable"))
}

func (c *cluster) findLocalServiceImport() *mcsv1a1.ServiceImport {
	list, err := c.localServiceImportClient.Namespace(test.LocalNamespace).List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Succeed())

	for i := range list.Items {
		if list.Items[i].GetLabels()[mcsv1a1.LabelServiceName] == c.service.Name &&
			list.Items[i].GetLabels()[constants.LabelSourceNamespace] == c.service.Namespace {
			serviceImport := &mcsv1a1.ServiceImport{}
			Expect(scheme.Scheme.Convert(&list.Items[i], serviceImport, nil)).To(Succeed())

			return serviceImport
		}
	}

	return nil
}

func (c *cluster) findLocalEndpointSlice() *discovery.EndpointSlice {
	return findEndpointSlice(c.localEndpointSliceClient, c.endpoints.Namespace, c.endpoints.Name, c.clusterID)
}

func (c *cluster) ensureNoEndpointSlice() {
	Consistently(func() interface{} {
		return findEndpointSlice(c.localEndpointSliceClient, c.endpoints.Namespace, c.endpoints.Name, c.clusterID)
	}, 300*time.Millisecond).Should(BeNil(), "Unexpected EndpointSlice")
}

func awaitServiceImport(client dynamic.NamespaceableResourceInterface, expected *mcsv1a1.ServiceImport) {
	var serviceImport *mcsv1a1.ServiceImport

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := client.Namespace(expected.Namespace).Get(context.TODO(), expected.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		serviceImport = &mcsv1a1.ServiceImport{}
		Expect(scheme.Scheme.Convert(obj, serviceImport, nil)).To(Succeed())

		return reflect.DeepEqual(&expected.Spec, &serviceImport.Spec) && reflect.DeepEqual(&expected.Status, &serviceImport.Status), nil
	})

	if !errors.Is(err, wait.ErrWaitTimeout) {
		Expect(err).To(Succeed())
	}

	if serviceImport == nil {
		Fail(fmt.Sprintf("ServiceImport %s/%s not found", expected.Namespace, expected.Name))
	}

	Expect(serviceImport.Spec).To(Equal(expected.Spec))
	Expect(serviceImport.Status).To(Equal(expected.Status))

	Expect(serviceImport.Labels).To(BeEmpty())
}

func findEndpointSlice(client dynamic.ResourceInterface, namespace, name, clusterID string) *discovery.EndpointSlice {
	list, err := client.List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Succeed())

	for i := range list.Items {
		if list.Items[i].GetLabels()[mcsv1a1.LabelServiceName] == name &&
			list.Items[i].GetLabels()[constants.LabelSourceNamespace] == namespace &&
			list.Items[i].GetLabels()[constants.MCSLabelSourceCluster] == clusterID {
			endpointSlice := &discovery.EndpointSlice{}
			Expect(scheme.Scheme.Convert(&list.Items[i], endpointSlice, nil)).To(Succeed())

			return endpointSlice
		}
	}

	return nil
}

func awaitEndpointSlice(client dynamic.ResourceInterface, expected *discovery.EndpointSlice) {
	var endpointSlice *discovery.EndpointSlice

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		endpointSlice = findEndpointSlice(client, expected.Namespace, expected.Name, expected.Labels[constants.MCSLabelSourceCluster])
		if endpointSlice == nil {
			return false, nil
		}

		return reflect.DeepEqual(expected.Endpoints, endpointSlice.Endpoints) &&
			reflect.DeepEqual(expected.Ports, endpointSlice.Ports), nil
	})

	if !errors.Is(err, wait.ErrWaitTimeout) {
		Expect(err).To(Succeed())
	}

	if endpointSlice == nil {
		Fail(fmt.Sprintf("EndpointSlice for %s/%s not found", expected.Namespace, expected.Name))
	}

	for k, v := range expected.Labels {
		Expect(endpointSlice.Labels).To(HaveKeyWithValue(k, v))
	}

	Expect(endpointSlice.AddressType).To(Equal(expected.AddressType))
	Expect(endpointSlice.Endpoints).To(Equal(expected.Endpoints))
	Expect(endpointSlice.Ports).To(Equal(expected.Ports))
}

func awaitNoEndpointSlice(client dynamic.ResourceInterface, ns, name, clusterID string) {
	Eventually(func() interface{} {
		return findEndpointSlice(client, ns, name, clusterID)
	}).Should(BeNil(), "Unexpected EndpointSlice found for %s/%s", ns, name)
}

func (c *cluster) dynamicServiceClientFor() dynamic.NamespaceableResourceInterface {
	return c.localDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"})
}

func (t *testDriver) awaitAggregatedServiceImport(sType mcsv1a1.ServiceImportType, name, ns string, clusters ...*cluster) {
	expServiceImport := &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", name, ns),
			Namespace: test.RemoteNamespace,
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  sType,
			Ports: []mcsv1a1.ServicePort{},
		},
	}

	if len(clusters) > 0 {
		if sType == mcsv1a1.ClusterSetIP {
			expServiceImport.Spec.Ports = t.aggregatedServicePorts
		}

		for _, c := range clusters {
			expServiceImport.Status.Clusters = append(expServiceImport.Status.Clusters,
				mcsv1a1.ClusterStatus{Cluster: c.clusterID})
		}
	}

	awaitServiceImport(t.brokerServiceImportClient, expServiceImport)

	expServiceImport.Name = name
	expServiceImport.Namespace = ns

	awaitServiceImport(t.cluster1.localServiceImportClient, expServiceImport)
	awaitServiceImport(t.cluster2.localServiceImportClient, expServiceImport)
}

func (t *testDriver) awaitNoAggregatedServiceImport(c *cluster) {
	test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace),
		fmt.Sprintf("%s-%s", c.service.Name, c.service.Namespace))
	test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(c.service.Namespace), c.service.Name)
	test.AwaitNoResource(t.cluster2.localServiceImportClient.Namespace(c.service.Namespace), c.service.Name)
}

func (t *testDriver) awaitEndpointSlice(c *cluster) {
	hasEndpoints := len(c.endpoints.Subsets[0].Addresses) > 0
	isHeadless := c.service.Spec.ClusterIP == corev1.ClusterIPNone

	expected := &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.service.Name,
			Namespace: c.service.Namespace,
			Labels: map[string]string{
				discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				constants.MCSLabelSourceCluster: c.clusterID,
				mcsv1a1.LabelServiceName:        c.service.Name,
				constants.LabelSourceNamespace:  c.service.Namespace,
				constants.LabelIsHeadless:       strconv.FormatBool(isHeadless),
			},
		},
		AddressType: discovery.AddressTypeIPv4,
	}

	if isHeadless {
		expected.Endpoints = c.endpointSliceAddresses

		for i := range c.endpoints.Subsets[0].Ports {
			expected.Ports = append(expected.Ports, discovery.EndpointPort{
				Name:        &c.endpoints.Subsets[0].Ports[i].Name,
				Protocol:    &c.endpoints.Subsets[0].Ports[i].Protocol,
				Port:        &c.endpoints.Subsets[0].Ports[i].Port,
				AppProtocol: c.endpoints.Subsets[0].Ports[i].AppProtocol,
			})
		}
	} else {
		expected.Endpoints = []discovery.Endpoint{
			{
				Addresses:  []string{c.serviceIP},
				Conditions: discovery.EndpointConditions{Ready: pointer.Bool(hasEndpoints)},
			},
		}

		for i := range c.service.Spec.Ports {
			expected.Ports = append(expected.Ports, discovery.EndpointPort{
				Name:        &c.service.Spec.Ports[i].Name,
				Protocol:    &c.service.Spec.Ports[i].Protocol,
				Port:        &c.service.Spec.Ports[i].Port,
				AppProtocol: c.service.Spec.Ports[i].AppProtocol,
			})
		}
	}

	awaitEndpointSlice(t.brokerEndpointSliceClient, expected)
	awaitEndpointSlice(t.cluster1.localEndpointSliceClient, expected)
	awaitEndpointSlice(t.cluster2.localEndpointSliceClient, expected)
}

func (t *testDriver) awaitNoEndpointSlice(c *cluster) {
	awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
	awaitNoEndpointSlice(t.brokerEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
	awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient, c.service.Namespace, c.service.Name, c.clusterID)
}

func endpointsClientFor(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(corev1.SchemeGroupVersion.WithResource("endpoints")).Namespace(namespace)
}

func serviceExportClientFor(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(schema.GroupVersionResource{
		Group:    mcsv1a1.GroupVersion.Group,
		Version:  mcsv1a1.GroupVersion.Version,
		Resource: "serviceexports",
	}).Namespace(namespace)
}

func endpointSliceClientFor(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(discovery.SchemeGroupVersion.WithResource("endpointslices")).Namespace(namespace)
}

func assertEquivalentConditions(actual, expected *mcsv1a1.ServiceExportCondition) {
	out := resource.ToJSON(actual)

	Expect(actual.Status).To(Equal(expected.Status), "Actual: %s", out)
	Expect(actual.LastTransitionTime).To(Not(BeNil()), "Actual: %s", out)
	Expect(actual.Reason).To(Not(BeNil()), "Actual: %s", out)
	Expect(*actual.Reason).To(Equal(*expected.Reason), "Actual: %s", out)
	Expect(actual.Message).To(Not(BeNil()), "Actual: %s", out)

	if expected.Message != nil {
		Expect(*actual.Message).To(Equal(*expected.Message), "Actual: %s", out)
	}
}

func toServiceExport(obj interface{}) *mcsv1a1.ServiceExport {
	se := &mcsv1a1.ServiceExport{}
	Expect(scheme.Scheme.Convert(obj, se, nil)).To(Succeed())

	return se
}

func (t *testDriver) awaitNonHeadlessServiceExported(clusters ...*cluster) {
	t.awaitServiceExported(mcsv1a1.ClusterSetIP, clusters...)
}

func (t *testDriver) awaitHeadlessServiceExported(clusters ...*cluster) {
	t.awaitServiceExported(mcsv1a1.Headless, clusters...)
}

func (t *testDriver) awaitServiceExported(sType mcsv1a1.ServiceImportType, clusters ...*cluster) {
	t.awaitAggregatedServiceImport(sType, t.cluster1.service.Name, t.cluster1.service.Namespace, clusters...)

	for _, c := range clusters {
		t.awaitEndpointSlice(c)

		c.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionTrue, ""))
		c.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "AwaitingExport"),
			newServiceExportSyncedCondition(corev1.ConditionTrue, ""))
	}
}

func (t *testDriver) awaitServiceUnexported(c *cluster) {
	t.awaitNoEndpointSlice(c)

	t.awaitNoAggregatedServiceImport(c)

	t.cluster1.localDynClient.Fake.ClearActions()

	_, err := endpointsClientFor(c.localDynClient, c.endpoints.Namespace).Get(context.TODO(), c.endpoints.Name, metav1.GetOptions{})
	if err == nil {
		// Ensure the service's Endpoints are no longer being watched by updating the Endpoints and verifying the
		// EndpointSlice isn't recreated.
		t.cluster1.localDynClient.Fake.ClearActions()

		c.endpoints.Subsets[0].Addresses = append(c.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.10"})
		c.updateEndpoints()

		testutil.EnsureNoActionsForResource(&t.cluster1.localDynClient.Fake, "endpointslices", "create")
	} else if !apierrors.IsNotFound(err) {
		Expect(err).To(Succeed())
	}
}

func newServiceExportValidCondition(status corev1.ConditionStatus, reason string) *mcsv1a1.ServiceExportCondition {
	return &mcsv1a1.ServiceExportCondition{
		Type:   mcsv1a1.ServiceExportValid,
		Status: status,
		Reason: &reason,
	}
}

func newServiceExportSyncedCondition(status corev1.ConditionStatus, reason string) *mcsv1a1.ServiceExportCondition {
	return &mcsv1a1.ServiceExportCondition{
		Type:   constants.ServiceExportSynced,
		Status: status,
		Reason: &reason,
	}
}

func newServiceExportConflictCondition(reason string) *mcsv1a1.ServiceExportCondition {
	return &mcsv1a1.ServiceExportCondition{
		Type:   mcsv1a1.ServiceExportConflict,
		Status: corev1.ConditionTrue,
		Reason: &reason,
	}
}

func setIngressIPConditions(ingressIP *unstructured.Unstructured, conditions ...metav1.Condition) {
	var err error

	condObjs := make([]interface{}, len(conditions))
	for i := range conditions {
		condObjs[i], err = runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		Expect(err).To(Succeed())
	}

	Expect(unstructured.SetNestedSlice(ingressIP.Object, condObjs, "status", "conditions")).To(Succeed())
}

func setIngressAllocatedIP(ingressIP *unstructured.Unstructured, ip string) {
	Expect(unstructured.SetNestedField(ingressIP.Object, ip, "status", "allocatedIP")).To(Succeed())
}

func toServicePort(port mcsv1a1.ServicePort) corev1.ServicePort {
	return corev1.ServicePort{
		Name:        port.Name,
		Protocol:    port.Protocol,
		Port:        port.Port,
		AppProtocol: port.AppProtocol,
	}
}
