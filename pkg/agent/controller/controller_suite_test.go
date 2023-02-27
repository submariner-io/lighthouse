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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	clusterID1       = "east"
	clusterID2       = "west"
	serviceNamespace = "service-ns"
	globalIP1        = "242.254.1.1"
	globalIP2        = "242.254.1.2"
	globalIP3        = "242.254.1.3"
)

var (
	nodeName = "my-node"
	hostName = "my-host"
	ready    = true
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
	agentSpec                controller.AgentSpecification
	localDynClient           dynamic.Interface
	localServiceExportClient *fake.DynamicResourceClient
	localServiceImportClient *fake.DynamicResourceClient
	localIngressIPClient     *fake.DynamicResourceClient
	localEndpointSliceClient dynamic.ResourceInterface
	agentController          *controller.Controller
	chanByCondType           map[mcsv1a1.ServiceExportConditionType]chan mcsv1a1.ServiceExportCondition
}

type testDriver struct {
	cluster1                  cluster
	cluster2                  cluster
	brokerServiceImportClient *fake.DynamicResourceClient
	brokerEndpointSliceClient *fake.DynamicResourceClient
	service                   *corev1.Service
	serviceExport             *mcsv1a1.ServiceExport
	endpoints                 *corev1.Endpoints
	stopCh                    chan struct{}
	syncerConfig              *broker.SyncerConfig
	endpointGlobalIPs         []string
	doStart                   bool
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

	t := &testDriver{
		cluster1: cluster{
			agentSpec: controller.AgentSpecification{
				ClusterID:        clusterID1,
				Namespace:        test.LocalNamespace,
				GlobalnetEnabled: false,
			},
		},
		cluster2: cluster{
			agentSpec: controller.AgentSpecification{
				ClusterID:        clusterID2,
				Namespace:        test.LocalNamespace,
				GlobalnetEnabled: false,
			},
		},
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nginx",
				Namespace: serviceNamespace,
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.253.9.1",
				Selector:  map[string]string{"app": "test"},
			},
		},
		syncerConfig: &broker.SyncerConfig{
			BrokerNamespace: test.RemoteNamespace,
			RestMapper: test.GetRESTMapperFor(&mcsv1a1.ServiceExport{}, &mcsv1a1.ServiceImport{}, &corev1.Service{},
				&corev1.Endpoints{}, &discovery.EndpointSlice{}, controller.GetGlobalIngressIPObj()),
			BrokerClient: fake.NewDynamicClient(syncerScheme),
			Scheme:       syncerScheme,
		},
		stopCh:  make(chan struct{}),
		doStart: true,
	}

	t.serviceExport = &mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.service.Name,
			Namespace: t.service.Namespace,
		},
	}

	t.endpoints = &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.service.Name,
			Namespace: t.service.Namespace,
			Labels:    map[string]string{"app": "test"},
		},
		Subsets: []corev1.EndpointSubset{
			{
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
				Ports: []corev1.EndpointPort{
					{
						Name:     "port-1",
						Protocol: corev1.ProtocolTCP,
						Port:     1234,
					},
				},
			},
		},
	}

	t.brokerServiceImportClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.RemoteNamespace).(*fake.DynamicResourceClient)

	t.brokerEndpointSliceClient = t.syncerConfig.BrokerClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace).(*fake.DynamicResourceClient)

	t.cluster1.init(t.syncerConfig)
	t.cluster2.init(t.syncerConfig)

	return t
}

func (t *testDriver) newGlobalIngressIP(name, ip string) *unstructured.Unstructured {
	ingressIP := controller.GetGlobalIngressIPObj()
	ingressIP.SetName(name)
	ingressIP.SetNamespace(t.service.Namespace)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.ClusterIPService, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, t.service.Name, "spec", "serviceRef", "name")).To(Succeed())

	setIngressAllocatedIP(ingressIP, ip)
	setIngressIPConditions(ingressIP, metav1.Condition{
		Type:    "Allocated",
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: "Allocated global IP",
	})

	return ingressIP
}

func (t *testDriver) newHeadlessGlobalIngressIP(name, ip string) *unstructured.Unstructured {
	ingressIP := t.newGlobalIngressIP("pod"+"-"+name, ip)
	Expect(unstructured.SetNestedField(ingressIP.Object, controller.HeadlessServicePod, "spec", "target")).To(Succeed())
	Expect(unstructured.SetNestedField(ingressIP.Object, name, "spec", "podRef", "name")).To(Succeed())

	return ingressIP
}

func (t *testDriver) justBeforeEach() {
	t.cluster1.start(t, *t.syncerConfig)
	t.cluster2.start(t, *t.syncerConfig)
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (c *cluster) init(syncerConfig *broker.SyncerConfig) {
	c.localDynClient = fake.NewDynamicClient(syncerConfig.Scheme)

	fake.AddDeleteCollectionReactor(&c.localDynClient.(*fake.DynamicClient).Fake,
		discovery.SchemeGroupVersion.WithKind("EndpointSlice"))

	c.localServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceExport{})).Namespace(serviceNamespace).(*fake.DynamicResourceClient)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(serviceNamespace)

	c.localIngressIPClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		controller.GetGlobalIngressIPObj())).Namespace(serviceNamespace).(*fake.DynamicResourceClient)

	c.chanByCondType = map[mcsv1a1.ServiceExportConditionType]chan mcsv1a1.ServiceExportCondition{}
	c.chanByCondType[mcsv1a1.ServiceExportValid] = make(chan mcsv1a1.ServiceExportCondition, 100)
	c.chanByCondType[constants.ServiceExportSynced] = make(chan mcsv1a1.ServiceExportCondition, 100)
}

//nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func (c *cluster) start(t *testDriver, syncerConfig broker.SyncerConfig) {
	processServiceExportConditions := func(oldObj, newObj interface{}) {
		defer GinkgoRecover()

		var oldSE *mcsv1a1.ServiceExport
		if oldObj != nil {
			oldSE = toServiceExport(oldObj)
		}

		newSE := toServiceExport(newObj)

		for i := range newSE.Status.Conditions {
			newCond := &newSE.Status.Conditions[i]

			condChan := c.chanByCondType[newCond.Type]
			if condChan == nil {
				continue
			}

			if oldSE != nil {
				prevCond := controller.FindServiceExportStatusCondition(oldSE.Status.Conditions, newCond.Type)
				if prevCond != nil && reflect.DeepEqual(prevCond, newCond) {
					continue
				}
			}

			condChan <- *newCond
		}
	}

	_, informer := cache.NewInformer(&cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return t.cluster1.localServiceExportClient.List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return t.cluster1.localServiceExportClient.Watch(context.TODO(), options)
		},
	}, &unstructured.Unstructured{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			processServiceExportConditions(nil, obj)
		},
		UpdateFunc: processServiceExportConditions,
	})

	go informer.Run(t.stopCh)
	Expect(cache.WaitForCacheSync(t.stopCh, informer.HasSynced)).To(BeTrue())

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

func findServiceImport(client dynamic.ResourceInterface, namespace, name, sourceLabel string) *mcsv1a1.ServiceImport {
	list, err := client.List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Succeed())

	for i := range list.Items {
		if list.Items[i].GetLabels()[sourceLabel] == name &&
			list.Items[i].GetLabels()[constants.LabelSourceNamespace] == namespace {
			serviceImport := &mcsv1a1.ServiceImport{}
			Expect(scheme.Scheme.Convert(&list.Items[i], serviceImport, nil)).To(Succeed())

			return serviceImport
		}
	}

	return nil
}

func awaitServiceImport(client dynamic.ResourceInterface, service *corev1.Service, sType mcsv1a1.ServiceImportType,
	serviceIP string,
) *mcsv1a1.ServiceImport {
	var serviceImport *mcsv1a1.ServiceImport

	Eventually(func() *mcsv1a1.ServiceImport {
		serviceImport = findServiceImport(client, service.Namespace, service.Name, mcsv1a1.LabelServiceName)
		return serviceImport
	}, 5*time.Second).ShouldNot(BeNil(), "ServiceImport not found")

	Expect(serviceImport.Spec.Type).To(Equal(sType))

	Expect(serviceImport.Status.Clusters).To(HaveLen(1))
	Expect(serviceImport.Status.Clusters[0].Cluster).To(Equal(clusterID1))

	if serviceIP == "" {
		Expect(len(serviceImport.Spec.IPs)).To(Equal(0))
	} else {
		Expect(serviceImport.Spec.IPs).To(Equal([]string{serviceIP}))
	}

	Expect(serviceImport.Spec.Ports).To(HaveLen(len(service.Spec.Ports)))

	for i := range service.Spec.Ports {
		Expect(serviceImport.Spec.Ports[i].Name).To(Equal(service.Spec.Ports[i].Name))
		Expect(serviceImport.Spec.Ports[i].Protocol).To(Equal(service.Spec.Ports[i].Protocol))
		Expect(serviceImport.Spec.Ports[i].Port).To(Equal(service.Spec.Ports[i].Port))
	}

	labels := serviceImport.GetObjectMeta().GetLabels()
	Expect(labels[constants.LabelSourceNamespace]).To(Equal(service.GetNamespace()))
	Expect(labels[mcsv1a1.LabelServiceName]).To(Equal(service.GetName()))
	Expect(labels[constants.MCSLabelSourceCluster]).To(Equal(clusterID1))

	return serviceImport
}

func (c *cluster) awaitServiceImport(service *corev1.Service, sType mcsv1a1.ServiceImportType, serviceIP string) *mcsv1a1.ServiceImport {
	return awaitServiceImport(c.localServiceImportClient, service, sType, serviceIP)
}

func awaitUpdatedServiceImport(client dynamic.ResourceInterface, service *corev1.Service, serviceIP string) {
	var serviceImport *mcsv1a1.ServiceImport

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		serviceImport = findServiceImport(client, service.Namespace, service.Name, mcsv1a1.LabelServiceName)
		Expect(serviceImport).ToNot(BeNil())

		if serviceIP == "" {
			return len(serviceImport.Spec.IPs) == 0, nil
		}

		return reflect.DeepEqual(serviceImport.Spec.IPs, []string{serviceIP}), nil
	})

	if errors.Is(err, wait.ErrWaitTimeout) {
		if serviceIP == "" {
			Expect(len(serviceImport.Spec.IPs)).To(Equal(0))
		} else {
			Expect(serviceImport.Spec.IPs).To(Equal([]string{serviceIP}))
		}
	}

	Expect(err).To(Succeed())
}

func (c *cluster) awaitUpdatedServiceImport(service *corev1.Service, serviceIP string) {
	awaitUpdatedServiceImport(c.localServiceImportClient, service, serviceIP)
}

func findEndpointSlice(client dynamic.ResourceInterface, namespace, name string) *discovery.EndpointSlice {
	list, err := client.List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Succeed())

	for i := range list.Items {
		if list.Items[i].GetLabels()[mcsv1a1.LabelServiceName] == name &&
			list.Items[i].GetLabels()[constants.LabelSourceNamespace] == namespace {
			endpointSlice := &discovery.EndpointSlice{}
			Expect(scheme.Scheme.Convert(&list.Items[i], endpointSlice, nil)).To(Succeed())

			return endpointSlice
		}
	}

	return nil
}

func awaitEndpointSlice(client dynamic.ResourceInterface, namespace, name string) *discovery.EndpointSlice {
	var endpointSlice *discovery.EndpointSlice

	Eventually(func() *discovery.EndpointSlice {
		endpointSlice = findEndpointSlice(client, namespace, name)
		return endpointSlice
	}, 5*time.Second).ShouldNot(BeNil(), "EndpointSlice not found")

	return endpointSlice
}

func awaitAndVerifyEndpointSlice(endpointSliceClient dynamic.ResourceInterface, endpoints *corev1.Endpoints,
	namespace string, globalIPs []string,
) *discovery.EndpointSlice {
	endpointSlice := awaitEndpointSlice(endpointSliceClient, endpoints.Namespace, endpoints.Name)

	Expect(endpointSlice.Namespace).To(Equal(namespace))

	labels := endpointSlice.GetLabels()
	Expect(labels).To(HaveKeyWithValue(discovery.LabelManagedBy, constants.LabelValueManagedBy))
	Expect(labels).To(HaveKeyWithValue(constants.MCSLabelSourceCluster, clusterID1))

	Expect(endpointSlice.AddressType).To(Equal(discovery.AddressTypeIPv4))

	addresses := globalIPs
	if addresses == nil {
		addresses = []string{
			endpoints.Subsets[0].Addresses[0].IP, endpoints.Subsets[0].Addresses[1].IP,
		}
	}

	Expect(endpointSlice.Endpoints).To(HaveLen(2))
	Expect(endpointSlice.Endpoints[0]).To(Equal(discovery.Endpoint{
		Addresses:  []string{addresses[0]},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Hostname:   &hostName,
	}))
	Expect(endpointSlice.Endpoints[1]).To(Equal(discovery.Endpoint{
		Addresses:  []string{addresses[1]},
		Hostname:   &endpoints.Subsets[0].Addresses[1].TargetRef.Name,
		Conditions: discovery.EndpointConditions{Ready: &ready},
		NodeName:   &nodeName,
	}))

	Expect(endpointSlice.Ports).To(HaveLen(1))

	name := "port-1"
	protocol := corev1.ProtocolTCP
	var port int32 = 1234

	Expect(endpointSlice.Ports[0]).To(Equal(discovery.EndpointPort{
		Name:     &name,
		Protocol: &protocol,
		Port:     &port,
	}))

	return endpointSlice
}

func (c *cluster) awaitEndpointSlice(t *testDriver) *discovery.EndpointSlice {
	return awaitAndVerifyEndpointSlice(c.localEndpointSliceClient, t.endpoints, t.service.Namespace, t.endpointGlobalIPs)
}

func awaitUpdatedEndpointSlice(endpointSliceClient dynamic.ResourceInterface, endpoints *corev1.Endpoints, expectedIPs []string) {
	sort.Strings(expectedIPs)

	var actualIPs []string

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		endpointSlice := findEndpointSlice(endpointSliceClient, endpoints.Namespace, endpoints.Name)
		Expect(endpointSlice).ToNot(BeNil())

		actualIPs = nil
		for _, ep := range endpointSlice.Endpoints {
			actualIPs = append(actualIPs, ep.Addresses...)
		}

		sort.Strings(actualIPs)

		return reflect.DeepEqual(actualIPs, expectedIPs), nil
	})

	if errors.Is(err, wait.ErrWaitTimeout) {
		Expect(actualIPs).To(Equal(expectedIPs))
	}

	Expect(err).To(Succeed())
}

func (c *cluster) awaitUpdatedEndpointSlice(endpoints *corev1.Endpoints, expectedIPs []string) {
	awaitUpdatedEndpointSlice(c.localEndpointSliceClient, endpoints, expectedIPs)
}

func (c *cluster) dynamicServiceClient() dynamic.NamespaceableResourceInterface {
	return c.localDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"})
}

func (t *testDriver) awaitBrokerServiceImport(sType mcsv1a1.ServiceImportType, serviceIP string) *mcsv1a1.ServiceImport {
	return awaitServiceImport(t.brokerServiceImportClient, t.service, sType, serviceIP)
}

func (t *testDriver) awaitBrokerEndpointSlice() *discovery.EndpointSlice {
	return awaitAndVerifyEndpointSlice(t.brokerEndpointSliceClient, t.endpoints, test.RemoteNamespace, t.endpointGlobalIPs)
}

func (t *testDriver) awaitUpdatedServiceImport(serviceIP string) {
	awaitUpdatedServiceImport(t.brokerServiceImportClient, t.service, serviceIP)
	t.cluster1.awaitUpdatedServiceImport(t.service, serviceIP)
	t.cluster2.awaitUpdatedServiceImport(t.service, serviceIP)
}

func (t *testDriver) awaitEndpointSlice() {
	t.awaitBrokerEndpointSlice()
	t.cluster1.awaitEndpointSlice(t)
	t.cluster2.awaitEndpointSlice(t)
}

func (t *testDriver) awaitUpdatedEndpointSlice(expectedIPs []string) {
	awaitUpdatedEndpointSlice(t.brokerEndpointSliceClient, t.endpoints, expectedIPs)
	t.cluster1.awaitUpdatedEndpointSlice(t.endpoints, expectedIPs)
	t.cluster2.awaitUpdatedEndpointSlice(t.endpoints, expectedIPs)
}

func (t *testDriver) createService() {
	test.CreateResource(t.cluster1.dynamicServiceClient().Namespace(t.service.Namespace), t.service)
}

func (t *testDriver) updateService() {
	test.UpdateResource(t.cluster1.dynamicServiceClient().Namespace(t.service.Namespace), t.service)
}

func (t *testDriver) createEndpoints() {
	test.CreateResource(t.dynamicEndpointsClient(), t.endpoints)
}

func (t *testDriver) updateEndpoints() {
	test.UpdateResource(t.dynamicEndpointsClient(), t.endpoints)
}

func (t *testDriver) dynamicEndpointsClient() dynamic.ResourceInterface {
	return dynamicEndpointsClient(t.cluster1.localDynClient, t.service.Namespace)
}

func dynamicEndpointsClient(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(corev1.SchemeGroupVersion.WithResource("endpoints")).Namespace(namespace)
}

func serviceExportClient(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(schema.GroupVersionResource{
		Group:    mcsv1a1.GroupVersion.Group,
		Version:  mcsv1a1.GroupVersion.Version,
		Resource: "serviceexports",
	}).Namespace(namespace)
}

func endpointSliceClient(client dynamic.Interface, namespace string) dynamic.ResourceInterface {
	return client.Resource(discovery.SchemeGroupVersion.WithResource("endpointslices")).Namespace(namespace)
}

func (t *testDriver) createServiceExport() {
	test.CreateResource(t.cluster1.localServiceExportClient, t.serviceExport)
}

func (t *testDriver) deleteServiceExport() {
	Expect(t.cluster1.localServiceExportClient.Delete(context.TODO(), t.service.GetName(), metav1.DeleteOptions{})).To(Succeed())
}

func (t *testDriver) deleteService() {
	Expect(t.cluster1.dynamicServiceClient().Namespace(t.service.Namespace).Delete(context.TODO(), t.service.Name,
		metav1.DeleteOptions{})).To(Succeed())
}

func (t *testDriver) createGlobalIngressIP(ingressIP *unstructured.Unstructured) {
	test.CreateResource(t.cluster1.localIngressIPClient, ingressIP)
}

func (t *testDriver) createEndpointIngressIPs() {
	t.endpointGlobalIPs = []string{globalIP1, globalIP2, globalIP3}
	t.createGlobalIngressIP(t.newHeadlessGlobalIngressIP("one", globalIP1))
	t.createGlobalIngressIP(t.newHeadlessGlobalIngressIP("two", globalIP2))
	t.createGlobalIngressIP(t.newHeadlessGlobalIngressIP("not-ready", globalIP3))
}

func (t *testDriver) awaitNoServiceImport(client dynamic.ResourceInterface) {
	test.AwaitNoResource(client, t.service.Name+"-"+t.service.Namespace+"-"+clusterID1)
}

func (t *testDriver) awaitNoEndpointSlice(client dynamic.ResourceInterface) {
	test.AwaitNoResource(client, t.endpoints.Name+"-"+clusterID1)
}

func assertEquivalentConditions(actual, expected *mcsv1a1.ServiceExportCondition) {
	out, _ := json.MarshalIndent(actual, "", "  ")

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

func (t *testDriver) awaitServiceExportCondition(expected ...*mcsv1a1.ServiceExportCondition) {
	conditionsEqual := func(actual, expected *mcsv1a1.ServiceExportCondition) bool {
		return actual != nil && actual.Type == expected.Type && actual.Status == expected.Status &&
			reflect.DeepEqual(actual.Reason, expected.Reason)
	}

	actual := make([]*mcsv1a1.ServiceExportCondition, len(expected))

	for i := range expected {
		actual[i] = t.retrieveServiceExportCondition(expected[i].Type)
		if conditionsEqual(actual[i], expected[i]) {
			continue
		}

		condChan := t.cluster1.chanByCondType[expected[i].Type]

		var received *mcsv1a1.ServiceExportCondition
		nIter := 0

		for received == nil && nIter <= 50 {
			select {
			case c := <-condChan:
				if conditionsEqual(&c, expected[i]) {
					received = &c
				}
			case <-time.After(100 * time.Millisecond):
			}

			nIter++
		}

		if received == nil {
			out, _ := json.MarshalIndent(expected[i], "", "  ")
			Fail(fmt.Sprintf("ServiceExport condition not received. Expected: %s", out))
		}

		actual[i] = received
	}

	for i := range expected {
		assertEquivalentConditions(actual[i], expected[i])
	}

	time.Sleep(time.Millisecond * 100)

	lastCond := expected[len(expected)-1]
	assertEquivalentConditions(t.retrieveServiceExportCondition(lastCond.Type), lastCond)
}

func (t *testDriver) ensureNoServiceExportCondition(condType mcsv1a1.ServiceExportConditionType) {
	Consistently(func() interface{} {
		return t.retrieveServiceExportCondition(condType)
	}).Should(BeNil(), "Unexpected ServiceExport status condition")
}

func (t *testDriver) retrieveServiceExportCondition(condType mcsv1a1.ServiceExportConditionType) *mcsv1a1.ServiceExportCondition {
	obj, err := t.cluster1.localServiceExportClient.Get(context.TODO(), t.service.Name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return controller.FindServiceExportStatusCondition(toServiceExport(obj).Status.Conditions, condType)
}

func (t *testDriver) awaitServiceExported(serviceIP string) {
	t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)

	t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, serviceIP)
	t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)

	t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionTrue, ""))
	t.awaitServiceExportCondition(newServiceExportSyncedCondition(corev1.ConditionFalse, "AwaitingSync"),
		newServiceExportSyncedCondition(corev1.ConditionTrue, ""))
}

func (t *testDriver) awaitHeadlessServiceImport() {
	serviceIP := ""
	t.awaitBrokerServiceImport(mcsv1a1.Headless, serviceIP)
	t.cluster1.awaitServiceImport(t.service, mcsv1a1.Headless, serviceIP)
	t.cluster2.awaitServiceImport(t.service, mcsv1a1.Headless, serviceIP)
}

func (t *testDriver) awaitServiceUnexported() {
	t.awaitNoServiceImport(t.brokerServiceImportClient)
	t.awaitNoServiceImport(t.cluster1.localServiceImportClient)
	t.awaitNoServiceImport(t.cluster2.localServiceImportClient)
}

func (t *testDriver) awaitHeadlessServiceUnexported() {
	t.awaitServiceUnexported()

	t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
	t.awaitNoEndpointSlice(t.brokerEndpointSliceClient)
	t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)

	// Ensure the service's Endpoints are no longer being watched by updating the Endpoints and verifying the
	// EndpointSlice isn't recreated.
	t.endpoints.Subsets[0].Addresses = append(t.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.10"})
	t.updateEndpoints()

	time.Sleep(200 * time.Millisecond)
	t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
}

func (t *testDriver) awaitServiceUnavailableStatus() {
	t.awaitServiceExportCondition(newServiceExportValidCondition(corev1.ConditionFalse, "ServiceUnavailable"))
}

func (t *testDriver) endpointIPs() []string {
	ips := []string{}
	for _, a := range t.endpoints.Subsets[0].Addresses {
		ips = append(ips, a.IP)
	}

	return ips
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
