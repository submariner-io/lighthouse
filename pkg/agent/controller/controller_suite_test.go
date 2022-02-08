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
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	fakeKubeClient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
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
	notReady = false
)

func init() {
	klog.InitFlags(nil)

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
	localKubeClient          kubernetes.Interface
	endpointsReactor         *fake.FailingReactor
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
}

func newTestDiver() *testDriver {
	syncerScheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(syncerScheme)).To(Succeed())
	Expect(discovery.AddToScheme(syncerScheme)).To(Succeed())
	Expect(mcsv1a1.AddToScheme(syncerScheme)).To(Succeed())

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
		stopCh: make(chan struct{}),
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

	c.localServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceExport{})).Namespace(serviceNamespace).(*fake.DynamicResourceClient)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(serviceNamespace)

	c.localIngressIPClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		controller.GetGlobalIngressIPObj())).Namespace(serviceNamespace).(*fake.DynamicResourceClient)

	fakeCS := fakeKubeClient.NewSimpleClientset()
	c.endpointsReactor = fake.NewFailingReactorForResource(&fakeCS.Fake, "endpoints")
	c.localKubeClient = fakeCS
}

// nolint:gocritic // (hugeParam) This function modifies syncerConf so we don't want to pass by pointer.
func (c *cluster) start(t *testDriver, syncerConfig broker.SyncerConfig) {
	syncerConfig.LocalClient = c.localDynClient
	bigint, err := rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceImportCounterName := "submariner_service_import" + bigint.String()

	bigint, err = rand.Int(rand.Reader, big.NewInt(1000000))
	Expect(err).To(Succeed())

	serviceExportCounterName := "submariner_service_export" + bigint.String()

	agentController, err := controller.New(&c.agentSpec, syncerConfig, c.localKubeClient,
		controller.AgentConfig{
			ServiceImportCounterName: serviceImportCounterName,
			ServiceExportCounterName: serviceExportCounterName,
		})

	Expect(err).To(Succeed())
	Expect(agentController.Start(t.stopCh)).To(Succeed())
}

func awaitServiceImport(client dynamic.ResourceInterface, service *corev1.Service, sType mcsv1a1.ServiceImportType,
	serviceIP string) *mcsv1a1.ServiceImport {
	obj := test.AwaitResource(client, service.Name+"-"+service.Namespace+"-"+clusterID1)

	serviceImport := &mcsv1a1.ServiceImport{}
	Expect(scheme.Scheme.Convert(obj, serviceImport, nil)).To(Succeed())

	Expect(serviceImport.GetAnnotations()["origin-name"]).To(Equal(service.Name))
	Expect(serviceImport.GetAnnotations()["origin-namespace"]).To(Equal(service.Namespace))
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
	Expect(labels[lhconstants.LabelSourceNamespace]).To(Equal(service.GetNamespace()))
	Expect(labels[lhconstants.LighthouseLabelSourceName]).To(Equal(service.GetName()))
	Expect(labels[lhconstants.LighthouseLabelSourceCluster]).To(Equal(clusterID1))

	return serviceImport
}

func (c *cluster) awaitServiceImport(service *corev1.Service, sType mcsv1a1.ServiceImportType, serviceIP string) *mcsv1a1.ServiceImport {
	return awaitServiceImport(c.localServiceImportClient, service, sType, serviceIP)
}

func awaitUpdatedServiceImport(client dynamic.ResourceInterface, service *corev1.Service, serviceIP string) {
	name := service.Name + "-" + service.Namespace + "-" + clusterID1

	var serviceImport *mcsv1a1.ServiceImport

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := client.Get(context.TODO(), name, metav1.GetOptions{})
		Expect(err).To(Succeed())

		serviceImport = &mcsv1a1.ServiceImport{}
		Expect(scheme.Scheme.Convert(obj, serviceImport, nil)).To(Succeed())

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

func awaitEndpointSlice(endpointSliceClient dynamic.ResourceInterface, endpoints *corev1.Endpoints,
	service *corev1.Service, namespace string, globalIPs []string) *discovery.EndpointSlice {
	obj := test.AwaitResource(endpointSliceClient, endpoints.Name+"-"+clusterID1)

	endpointSlice := &discovery.EndpointSlice{}
	Expect(scheme.Scheme.Convert(obj, endpointSlice, nil)).To(Succeed())
	Expect(endpointSlice.Namespace).To(Equal(namespace))

	labels := endpointSlice.GetLabels()
	Expect(labels).To(HaveKeyWithValue(discovery.LabelManagedBy, lhconstants.LabelValueManagedBy))
	Expect(labels).To(HaveKeyWithValue(lhconstants.LabelSourceNamespace, service.Namespace))
	Expect(labels).To(HaveKeyWithValue(lhconstants.MCSLabelSourceCluster, clusterID1))
	Expect(labels).To(HaveKeyWithValue(lhconstants.MCSLabelServiceName, service.Name))

	Expect(endpointSlice.AddressType).To(Equal(discovery.AddressTypeIPv4))

	addresses := globalIPs
	if addresses == nil {
		addresses = []string{
			endpoints.Subsets[0].Addresses[0].IP, endpoints.Subsets[0].Addresses[1].IP,
			endpoints.Subsets[0].NotReadyAddresses[0].IP,
		}
	}

	Expect(endpointSlice.Endpoints).To(HaveLen(3))
	Expect(endpointSlice.Endpoints[0]).To(Equal(discovery.Endpoint{
		Addresses:  []string{addresses[0]},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Hostname:   &hostName,
	}))
	Expect(endpointSlice.Endpoints[1]).To(Equal(discovery.Endpoint{
		Addresses:  []string{addresses[1]},
		Hostname:   &endpoints.Subsets[0].Addresses[1].TargetRef.Name,
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Topology:   map[string]string{"kubernetes.io/hostname": nodeName},
	}))
	Expect(endpointSlice.Endpoints[2]).To(Equal(discovery.Endpoint{
		Addresses:  []string{addresses[2]},
		Hostname:   &endpoints.Subsets[0].NotReadyAddresses[0].TargetRef.Name,
		Conditions: discovery.EndpointConditions{Ready: &notReady},
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
	return awaitEndpointSlice(c.localEndpointSliceClient, t.endpoints, t.service, t.service.Namespace, t.endpointGlobalIPs)
}

func awaitUpdatedEndpointSlice(endpointSliceClient dynamic.ResourceInterface, endpoints *corev1.Endpoints, expectedIPs []string) {
	name := endpoints.Name + "-" + clusterID1

	sort.Strings(expectedIPs)

	var actualIPs []string

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := endpointSliceClient.Get(context.TODO(), name, metav1.GetOptions{})
		Expect(err).To(Succeed())

		endpointSlice := &discovery.EndpointSlice{}
		Expect(scheme.Scheme.Convert(obj, endpointSlice, nil)).To(Succeed())

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

func (t *testDriver) awaitBrokerServiceImport(sType mcsv1a1.ServiceImportType, serviceIP string) *mcsv1a1.ServiceImport {
	return awaitServiceImport(t.brokerServiceImportClient, t.service, sType, serviceIP)
}

func (t *testDriver) awaitBrokerEndpointSlice() *discovery.EndpointSlice {
	return awaitEndpointSlice(t.brokerEndpointSliceClient, t.endpoints, t.service, test.RemoteNamespace, t.endpointGlobalIPs)
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
	_, err := t.cluster1.localKubeClient.CoreV1().Services(t.service.Namespace).Create(context.TODO(), t.service, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	test.CreateResource(t.dynamicServiceClient(), t.service)
}

func (t *testDriver) createEndpoints() {
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Create(context.TODO(), t.endpoints, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	test.CreateResource(t.dynamicEndpointsClient(), t.endpoints)
}

func (t *testDriver) updateEndpoints() {
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Update(context.TODO(), t.endpoints, metav1.UpdateOptions{})
	Expect(err).To(Succeed())

	test.UpdateResource(t.dynamicEndpointsClient(), t.endpoints)
}

func (t *testDriver) dynamicEndpointsClient() dynamic.ResourceInterface {
	return t.cluster1.localDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}).Namespace(t.service.Namespace)
}

func (t *testDriver) createServiceExport() {
	test.CreateResource(t.cluster1.localServiceExportClient, t.serviceExport)
}

func (t *testDriver) deleteServiceExport() {
	Expect(t.cluster1.localServiceExportClient.Delete(context.TODO(), t.service.GetName(), metav1.DeleteOptions{})).To(Succeed())
}

func (t *testDriver) deleteService() {
	Expect(t.dynamicServiceClient().Delete(context.TODO(), t.service.Name, metav1.DeleteOptions{})).To(Succeed())

	Expect(t.cluster1.localKubeClient.CoreV1().Services(t.service.Namespace).Delete(context.TODO(), t.service.Name,
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

func (t *testDriver) dynamicServiceClient() dynamic.ResourceInterface {
	return t.cluster1.localDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).Namespace(t.service.Namespace)
}

func (t *testDriver) awaitNoServiceImport(client dynamic.ResourceInterface) {
	test.AwaitNoResource(client, t.service.Name+"-"+t.service.Namespace+"-"+clusterID1)
}

func (t *testDriver) awaitNoEndpointSlice(client dynamic.ResourceInterface) {
	test.AwaitNoResource(client, t.endpoints.Name+"-"+clusterID1)
}

func (t *testDriver) awaitServiceExportStatus(atIndex int, expCond ...*mcsv1a1.ServiceExportCondition) {
	var found *mcsv1a1.ServiceExport

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := t.cluster1.localServiceExportClient.Get(context.TODO(), t.service.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			return false, err
		}

		se := &mcsv1a1.ServiceExport{}
		Expect(scheme.Scheme.Convert(obj, se, nil)).To(Succeed())

		found = se

		if (atIndex + len(expCond)) != len(se.Status.Conditions) {
			return false, nil
		}

		j := atIndex
		for _, exp := range expCond {
			actual := se.Status.Conditions[j]
			j++
			Expect(actual.Type).To(Equal(exp.Type))
			Expect(actual.Status).To(Equal(exp.Status))
			Expect(actual.LastTransitionTime).To(Not(BeNil()))
			Expect(actual.Reason).To(Not(BeNil()))
			Expect(*actual.Reason).To(Equal(*exp.Reason))
			Expect(actual.Message).To(Not(BeNil()))

			if exp.Message != nil {
				Expect(*actual.Message).To(Equal(*exp.Message))
			}
		}

		return true, nil
	})

	if errors.Is(err, wait.ErrWaitTimeout) {
		if found == nil {
			Fail("ServiceExport not found")
		}

		Fail(format.Message(found.Status.Conditions, fmt.Sprintf("to contain at index %d", atIndex), expCond))
	} else {
		Expect(err).To(Succeed())
	}
}

func (t *testDriver) awaitNotServiceExportStatus(notCond *mcsv1a1.ServiceExportCondition) {
	err := wait.PollImmediate(50*time.Millisecond, 300*time.Millisecond, func() (bool, error) {
		obj, err := t.cluster1.localServiceExportClient.Get(context.TODO(), t.service.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		se := &mcsv1a1.ServiceExport{}
		Expect(scheme.Scheme.Convert(obj, se, nil)).To(Succeed())

		if len(se.Status.Conditions) == 0 {
			return false, nil
		}

		last := &se.Status.Conditions[len(se.Status.Conditions)-1]
		if last.Message == notCond.Message && last.Status == notCond.Status {
			return false, fmt.Errorf("Received unexpected %#v", last)
		}

		return false, nil
	})

	if !errors.Is(err, wait.ErrWaitTimeout) {
		Fail(err.Error())
	}
}

func (t *testDriver) awaitServiceExported(serviceIP string, statusIndex int) int {
	t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)
	t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, serviceIP)
	t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)

	t.awaitServiceExportStatus(statusIndex, newServiceExportCondition(corev1.ConditionFalse, "AwaitingSync"),
		newServiceExportCondition(corev1.ConditionTrue, ""))

	return statusIndex + 2
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

	// Ensure the service's Endpoints are no longer being watched by updating the Endpoints and verifyjng the
	// EndpointSlice isn't recreated.
	t.endpoints.Subsets[0].Addresses = append(t.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.10"})
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Update(context.TODO(), t.endpoints, metav1.UpdateOptions{})
	Expect(err).To(Succeed())

	time.Sleep(200 * time.Millisecond)
	t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
}

func (t *testDriver) awaitServiceUnavailableStatus(atIndex int) {
	t.awaitServiceExportStatus(atIndex, newServiceExportCondition(corev1.ConditionFalse, "ServiceUnavailable"))
}

func (t *testDriver) endpointIPs() []string {
	ips := []string{}
	for _, a := range t.endpoints.Subsets[0].Addresses {
		ips = append(ips, a.IP)
	}

	return ips
}

func newServiceExportCondition(status corev1.ConditionStatus, reason string) *mcsv1a1.ServiceExportCondition {
	return &mcsv1a1.ServiceExportCondition{
		Type:   mcsv1a1.ServiceExportValid,
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
