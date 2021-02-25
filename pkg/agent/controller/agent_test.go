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
package controller_test

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	fakeKubeClient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const clusterID1 = "east"
const clusterID2 = "west"
const serviceNamespace = "service-ns"

var nodeName = "my-node"
var hostName = "my-host"
var ready = true
var notReady = false

var _ = Describe("ServiceImport syncing", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		When("the Service already exists", func() {
			It("should correctly sync a ServiceImport and update the ServiceExport status", func() {
				t.createService()
				t.createServiceExport()
				t.awaitServiceExported(t.service.Spec.ClusterIP, 0)
			})
		})

		When("the Service doesn't initially exist", func() {
			It("should initially update the ServiceExport status to Initialized and eventually sync a ServiceImport", func() {
				t.createServiceExport()
				t.awaitServiceUnavailableStatus(0)

				t.createService()
				t.awaitServiceExported(t.service.Spec.ClusterIP, 1)
			})
		})
	})

	When("a ServiceExport is deleted after a ServiceImport is synced", func() {
		It("should delete the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)

			t.deleteServiceExport()
			t.awaitServiceUnexported()
		})
	})

	When("an exported Service is deleted and recreated while the ServiceExport still exists", func() {
		It("should delete and recreate the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			nextStatusIndex := t.awaitServiceExported(t.service.Spec.ClusterIP, 0)

			t.deleteService()
			t.awaitServiceUnexported()
			t.awaitServiceUnavailableStatus(nextStatusIndex)

			t.createService()
			t.awaitServiceExported(t.service.Spec.ClusterIP, nextStatusIndex+1)
		})
	})

	When("the ServiceImport sync initially fails", func() {
		BeforeEach(func() {
			t.cluster1.localServiceImportClient.PersistentFailOnCreate.Store("mock create error")
		})

		It("should not update the ServiceExport status to Exported until the sync is successful", func() {
			t.createService()
			t.createServiceExport()

			message := "AwaitingSync"
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionFalse, message))

			t.awaitNotServiceExportStatus(&mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportValid,
				Status:  corev1.ConditionTrue,
				Message: &message,
			})

			t.cluster1.localServiceImportClient.PersistentFailOnCreate.Store("")
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)
		})
	})

	When("the ServiceExportCondition list count reaches MaxExportStatusConditions", func() {
		var oldMaxExportStatusConditions int

		BeforeEach(func() {
			oldMaxExportStatusConditions = controller.MaxExportStatusConditions
			controller.MaxExportStatusConditions = 1
		})

		AfterEach(func() {
			controller.MaxExportStatusConditions = oldMaxExportStatusConditions
		})

		It("should correctly truncate the ServiceExportCondition list", func() {
			t.createService()
			t.createServiceExport()

			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionTrue, ""))
		})
	})

	When("a ServiceExport is created for a Service whose type is other than ServiceTypeClusterIP", func() {
		BeforeEach(func() {
			t.service.Spec.Type = corev1.ServiceTypeNodePort
		})

		It("should update the ServiceExport status and not sync a ServiceImport", func() {
			t.createService()
			t.createServiceExport()

			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionFalse, "UnsupportedServiceType"))
			t.awaitNoServiceImport(t.brokerServiceImportClient)
		})
	})

	When("a Service has port information", func() {
		BeforeEach(func() {
			t.service.Spec.Ports = []corev1.ServicePort{
				{
					Name:     "eth0",
					Protocol: corev1.ProtocolTCP,
					Port:     123,
				},
				{
					Name:     "eth1",
					Protocol: corev1.ProtocolTCP,
					Port:     1234,
				},
			}
		})

		It("should set the appropriate port information in the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)
		})
	})
})

var _ = Describe("Globalnet enabled", func() {
	globalIP := "192.168.10.34"
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
		t.cluster1.agentSpec.GlobalnetEnabled = true
		t.cluster2.agentSpec.GlobalnetEnabled = true
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a local ServiceExport is created and the Service has a global IP", func() {
		BeforeEach(func() {
			t.service.SetAnnotations(map[string]string{"submariner.io/globalIp": globalIP})
		})

		It("should sync a ServiceImport with the global IP of the Service", func() {
			t.awaitServiceExported(globalIP, 0)
		})
	})

	When("a local ServiceExport is created and the Service does not initially have a global IP", func() {
		It("should eventually sync a ServiceImport with the global IP of the Service", func() {
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionFalse, "ServiceGlobalIPUnavailable"))

			t.service.SetAnnotations(map[string]string{"submariner.io/globalIp": globalIP})
			test.UpdateResource(t.dynamicServiceClient(), t.service)

			t.awaitServiceExported(globalIP, 1)
		})
	})

	When("a ServiceExport is created for a headless Service", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
		})

		It("should update the ServiceExport status and not sync a ServiceImport", func() {
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionFalse, "UnsupportedServiceType"))

			t.awaitNoServiceImport(t.brokerServiceImportClient)
		})
	})
})

var _ = Describe("Headless service syncing", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
		t.service.Spec.ClusterIP = corev1.ClusterIPNone
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("a ServiceExport is created", func() {
		When("the Endpoints already exists", func() {
			It("should correctly sync a ServiceImport and EndpointSlice", func() {
				t.createEndpoints()
				t.createServiceExport()

				t.awaitHeadlessServiceImport("")
				t.awaitEndpointSlice()
			})
		})

		When("the Endpoints doesn't initially exist", func() {
			It("should eventually sync a correct ServiceImport and EndpointSlice", func() {
				t.createServiceExport()
				t.awaitHeadlessServiceImport("")

				t.createEndpoints()
				t.awaitUpdatedServiceImport("")
				t.awaitEndpointSlice()
			})
		})
	})

	When("the Endpoints for a service are updated", func() {
		It("should update the ServiceImport and EndpointSlice", func() {
			t.createEndpoints()
			t.createServiceExport()

			t.awaitHeadlessServiceImport("")
			t.awaitEndpointSlice()

			t.endpoints.Subsets[0].Addresses = append(t.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.3"})
			t.updateEndpoints()
			t.awaitUpdatedServiceImport("")
			t.awaitUpdatedEndpointSlice(append(t.endpointIPs(), "10.253.6.1"))
		})
	})

	When("a ServiceExport is deleted", func() {
		It("should delete the ServiceImport and EndpointSlice", func() {
			t.createEndpoints()
			t.createServiceExport()
			t.awaitHeadlessServiceImport("")
			t.awaitEndpointSlice()

			t.deleteServiceExport()
			t.awaitHeadlessServiceUnexported()
		})
	})
})

var _ = Describe("Service export failures", func() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDiver()
	})

	JustBeforeEach(func() {
		t.justBeforeEach()
		t.createService()
		t.createEndpoints()
		t.createServiceExport()
	})

	AfterEach(func() {
		t.afterEach()
	})

	When("Endpoints retrieval initially fails", func() {
		BeforeEach(func() {
			t.service.Spec.ClusterIP = corev1.ClusterIPNone
			t.cluster1.endpointsReactor.SetFailOnGet(errors.New("fake Get error"))
		})

		It("should update the ServiceExport status and eventually sync a ServiceImport", func() {
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionUnknown, "ServiceRetrievalFailed"))
			t.cluster1.endpointsReactor.SetResetOnFailure(true)
			t.awaitHeadlessServiceImport("")
		})
	})

	When("a conflict initially occurs when updating the ServiceExport status", func() {
		BeforeEach(func() {
			t.cluster1.localServiceExportClient.FailOnUpdate = apierrors.NewConflict(schema.GroupResource{}, t.serviceExport.Name,
				errors.New("fake conflict"))
		})

		It("should eventually update the ServiceExport status", func() {
			t.awaitServiceExported(t.service.Spec.ClusterIP, 0)
		})
	})
})

type cluster struct {
	agentSpec                controller.AgentSpecification
	localDynClient           dynamic.Interface
	localServiceExportClient *fake.DynamicResourceClient
	localServiceImportClient *fake.DynamicResourceClient
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
			RestMapper: test.GetRESTMapperFor(&mcsv1a1.ServiceExport{}, &mcsv1a1.ServiceImport{},
				&corev1.Service{}, &corev1.Endpoints{}, &discovery.EndpointSlice{}),
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
					},
					{
						IP:       "192.168.5.2",
						NodeName: &nodeName,
					},
				},
				NotReadyAddresses: []corev1.EndpointAddress{
					{
						IP: "10.253.6.1",
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

	t.cluster1.init(*t.syncerConfig)
	t.cluster2.init(*t.syncerConfig)

	return t
}

func (t *testDriver) justBeforeEach() {
	t.cluster1.start(t, *t.syncerConfig)
	t.cluster2.start(t, *t.syncerConfig)
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (c *cluster) init(syncerConfig broker.SyncerConfig) {
	c.localDynClient = fake.NewDynamicClient(syncerConfig.Scheme)

	c.localServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceExport{})).Namespace(serviceNamespace).(*fake.DynamicResourceClient)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(syncerConfig.RestMapper,
		&discovery.EndpointSlice{})).Namespace(serviceNamespace)

	fakeCS := fakeKubeClient.NewSimpleClientset()
	c.endpointsReactor = fake.NewFailingReactorForResource(&fakeCS.Fake, "endpoints")
	c.localKubeClient = fakeCS

	fake.AddDeleteCollectionReactor(&fakeCS.Fake, "EndpointSlice")
}

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
			ServiceExportCounterName: serviceExportCounterName})

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
	Expect(labels[lhconstants.LabelSourceName]).To(Equal(service.GetName()))
	Expect(labels[lhconstants.LabelSourceCluster]).To(Equal(clusterID1))

	return serviceImport
}

func (c *cluster) awaitServiceImport(service *corev1.Service, sType mcsv1a1.ServiceImportType, serviceIP string) {
	awaitServiceImport(c.localServiceImportClient, service, sType, serviceIP)
}

func awaitUpdatedServiceImport(client dynamic.ResourceInterface, service *corev1.Service,
	serviceIP string) *mcsv1a1.ServiceImport {
	name := service.Name + "-" + service.Namespace + "-" + clusterID1

	var serviceImport *mcsv1a1.ServiceImport

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := client.Get(name, metav1.GetOptions{})
		Expect(err).To(Succeed())

		serviceImport = &mcsv1a1.ServiceImport{}
		Expect(scheme.Scheme.Convert(obj, serviceImport, nil)).To(Succeed())

		if serviceIP == "" {
			return len(serviceImport.Spec.IPs) == 0, nil
		} else {
			return reflect.DeepEqual(serviceImport.Spec.IPs, []string{serviceIP}), nil
		}
	})

	if err == wait.ErrWaitTimeout {
		if serviceIP == "" {
			Expect(len(serviceImport.Spec.IPs)).To(Equal(0))
		} else {
			Expect(serviceImport.Spec.IPs).To(Equal([]string{serviceIP}))
		}
	}

	Expect(err).To(Succeed())

	return serviceImport
}

func (c *cluster) awaitUpdatedServiceImport(service *corev1.Service, serviceIP string) {
	awaitUpdatedServiceImport(c.localServiceImportClient, service, serviceIP)
}

func awaitEndpointSlice(endpointSliceClient, serviceImportClient dynamic.ResourceInterface,
	endpoints *corev1.Endpoints, service *corev1.Service, namespace string, expectOwnerRef bool) {
	obj := test.AwaitResource(endpointSliceClient, endpoints.Name+"-"+clusterID1)

	endpointSlice := &discovery.EndpointSlice{}
	Expect(scheme.Scheme.Convert(obj, endpointSlice, nil)).To(Succeed())

	siName := service.Name + "-" + service.Namespace + "-" + clusterID1

	Expect(endpointSlice.Namespace).To(Equal(namespace))

	labels := endpointSlice.GetLabels()
	Expect(labels).To(HaveKeyWithValue(lhconstants.LabelServiceImportName, siName))
	Expect(labels).To(HaveKeyWithValue(discovery.LabelManagedBy, lhconstants.LabelValueManagedBy))
	Expect(labels).To(HaveKeyWithValue(lhconstants.LabelSourceNamespace, service.Namespace))
	Expect(labels).To(HaveKeyWithValue(lhconstants.LabelSourceCluster, clusterID1))

	if expectOwnerRef {
		Expect(endpointSlice.OwnerReferences).To(HaveLen(1))

		si, err := serviceImportClient.Get(siName, metav1.GetOptions{})
		Expect(err).To(Succeed())

		controllerFlag := false

		Expect(endpointSlice.OwnerReferences[0]).To(Equal(metav1.OwnerReference{
			APIVersion: "multicluster.x-k8s.io.v1alpha1",
			Kind:       "ServiceImport",
			Name:       siName,
			UID:        si.GetUID(),
			Controller: &controllerFlag,
		}))
	} else {
		Expect(endpointSlice.OwnerReferences).To(HaveLen(0))
	}

	Expect(endpointSlice.AddressType).To(Equal(discovery.AddressTypeIPv4))

	Expect(endpointSlice.Endpoints).To(HaveLen(3))
	Expect(endpointSlice.Endpoints[0]).To(Equal(discovery.Endpoint{
		Addresses:  []string{"192.168.5.1"},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Hostname:   &hostName,
	}))
	Expect(endpointSlice.Endpoints[1]).To(Equal(discovery.Endpoint{
		Addresses:  []string{"192.168.5.2"},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Topology:   map[string]string{"kubernetes.io/hostname": nodeName},
	}))
	Expect(endpointSlice.Endpoints[2]).To(Equal(discovery.Endpoint{
		Addresses:  []string{"10.253.6.1"},
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
}

func (c *cluster) awaitEndpointSlice(endpoints *corev1.Endpoints, service *corev1.Service) {
	awaitEndpointSlice(c.localEndpointSliceClient, c.localServiceImportClient, endpoints, service,
		service.Namespace, c.agentSpec.ClusterID == clusterID1)
}

func awaitUpdatedEndpointSlice(endpointSliceClient dynamic.ResourceInterface, endpoints *corev1.Endpoints, expectedIPs []string) {
	name := endpoints.Name + "-" + clusterID1

	sort.Strings(expectedIPs)

	var actualIPs []string

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := endpointSliceClient.Get(name, metav1.GetOptions{})
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

	if err == wait.ErrWaitTimeout {
		Expect(actualIPs).To(Equal(expectedIPs))
	}

	Expect(err).To(Succeed())
}

func (c *cluster) awaitUpdatedEndpointSlice(endpoints *corev1.Endpoints, expectedIPs []string) {
	awaitUpdatedEndpointSlice(c.localEndpointSliceClient, endpoints, expectedIPs)
}

func (t *testDriver) awaitBrokerServiceImport(sType mcsv1a1.ServiceImportType, serviceIP string) {
	awaitServiceImport(t.brokerServiceImportClient, t.service, sType, serviceIP)
}

func (t *testDriver) awaitUpdatedServiceImport(serviceIP string) {
	awaitUpdatedServiceImport(t.brokerServiceImportClient, t.service, serviceIP)
	t.cluster1.awaitUpdatedServiceImport(t.service, serviceIP)
	t.cluster2.awaitUpdatedServiceImport(t.service, serviceIP)
}

func (t *testDriver) awaitEndpointSlice(serviceIPs ...string) {
	awaitEndpointSlice(t.brokerEndpointSliceClient, t.brokerServiceImportClient, t.endpoints, t.service, test.RemoteNamespace, false)
	t.cluster1.awaitEndpointSlice(t.endpoints, t.service)
	t.cluster2.awaitEndpointSlice(t.endpoints, t.service)
}

func (t *testDriver) awaitUpdatedEndpointSlice(expectedIPs []string) {
	awaitUpdatedEndpointSlice(t.brokerEndpointSliceClient, t.endpoints, expectedIPs)
	t.cluster1.awaitUpdatedEndpointSlice(t.endpoints, expectedIPs)
	t.cluster2.awaitUpdatedEndpointSlice(t.endpoints, expectedIPs)
}

func (t *testDriver) createService() {
	_, err := t.cluster1.localKubeClient.CoreV1().Services(t.service.Namespace).Create(t.service)
	Expect(err).To(Succeed())

	test.CreateResource(t.dynamicServiceClient(), t.service)
}

func (t *testDriver) createEndpoints() {
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Create(t.endpoints)
	Expect(err).To(Succeed())

	test.CreateResource(t.dynamicEndpointsClient(), t.endpoints)
}

func (t *testDriver) updateEndpoints() {
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Update(t.endpoints)
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
	Expect(t.cluster1.localServiceExportClient.Delete(t.service.GetName(), nil)).To(Succeed())
}

func (t *testDriver) deleteService() {
	Expect(t.dynamicServiceClient().Delete(t.service.Name, nil)).To(Succeed())

	Expect(t.cluster1.localKubeClient.CoreV1().Services(t.service.Namespace).Delete(t.service.Name, nil)).To(Succeed())
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
		obj, err := t.cluster1.localServiceExportClient.Get(t.service.Name, metav1.GetOptions{})
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
		}

		return true, nil
	})

	if err == wait.ErrWaitTimeout {
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
		obj, err := t.cluster1.localServiceExportClient.Get(t.service.Name, metav1.GetOptions{})
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

	if err != wait.ErrWaitTimeout {
		Fail(err.Error())
	}
}

func (t *testDriver) awaitServiceExported(serviceIP string, statusIndex int) int {
	t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)
	t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, serviceIP)
	t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)

	t.awaitServiceExportStatus(statusIndex, newServiceExportCondition(mcsv1a1.ServiceExportValid,
		corev1.ConditionFalse, "AwaitingSync"), newServiceExportCondition(mcsv1a1.ServiceExportValid,
		corev1.ConditionTrue, ""))

	return statusIndex + 2
}

func (t *testDriver) awaitHeadlessServiceImport(serviceIP string) {
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

	// In a real k8s env, the EndpointSlice would be deleted via k8s GC as it is owned by the ServiceImport.
	Expect(t.cluster1.localEndpointSliceClient.Delete(t.endpoints.Name+"-"+clusterID1, nil)).To(Succeed())

	t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
	t.awaitNoEndpointSlice(t.brokerEndpointSliceClient)
	t.awaitNoEndpointSlice(t.cluster2.localEndpointSliceClient)

	// Ensure the service's Endpoints are no longer being watched by updating the Endpoints and verifyjng the
	// EndpointSlice isn't recreated.
	t.endpoints.Subsets[0].Addresses = append(t.endpoints.Subsets[0].Addresses, corev1.EndpointAddress{IP: "192.168.5.10"})
	_, err := t.cluster1.localKubeClient.CoreV1().Endpoints(t.endpoints.Namespace).Update(t.endpoints)
	Expect(err).To(Succeed())

	time.Sleep(200 * time.Millisecond)
	t.awaitNoEndpointSlice(t.cluster1.localEndpointSliceClient)
}

func (t *testDriver) awaitServiceUnavailableStatus(atIndex int) {
	t.awaitServiceExportStatus(atIndex, newServiceExportCondition(mcsv1a1.ServiceExportValid,
		corev1.ConditionFalse, "ServiceUnavailable"))
}

func (t *testDriver) endpointIPs() []string {
	ips := []string{}
	for _, a := range t.endpoints.Subsets[0].Addresses {
		ips = append(ips, a.IP)
	}

	return ips
}

func newServiceExportCondition(cType mcsv1a1.ServiceExportConditionType,
	status corev1.ConditionStatus, reason string) *mcsv1a1.ServiceExportCondition {
	return &mcsv1a1.ServiceExportCondition{
		Type:   cType,
		Status: status,
		Reason: &reason,
	}
}
