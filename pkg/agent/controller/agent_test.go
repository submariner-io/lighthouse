package controller_test

import (
	"errors"
	"fmt"
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
	"k8s.io/apimachinery/pkg/api/meta"
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

	When("an exported Service is deleted while the ServiceExport still exists", func() {
		It("should delete the ServiceImport", func() {
			t.createService()
			t.createServiceExport()
			nextStatusIndex := t.awaitServiceExported(t.service.Spec.ClusterIP, 0)

			t.deleteService()
			t.awaitServiceUnexported()
			t.awaitServiceUnavailableStatus(nextStatusIndex)
		})
	})

	When("the ServiceImport sync initially fails", func() {
		BeforeEach(func() {
			t.brokerServiceImportClient.PersistentFailOnCreate.Store("mock create error")
		})

		It("should not update the ServiceExport status to Exported until the sync is successful", func() {
			t.createService()
			t.createServiceExport()

			message := "AwaitingSync"
			t.awaitServiceExportStatus(0, newServiceExportCondition(mcsv1a1.ServiceExportValid,
				corev1.ConditionTrue, message))

			t.awaitNotServiceExportStatus(&mcsv1a1.ServiceExportCondition{
				Type:    mcsv1a1.ServiceExportValid,
				Status:  corev1.ConditionTrue,
				Message: &message,
			})

			t.brokerServiceImportClient.PersistentFailOnCreate.Store("")
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
	localServiceImportClient dynamic.ResourceInterface
	localEndpointSliceClient dynamic.ResourceInterface
	localKubeClient          kubernetes.Interface
	endpointsReactor         *fake.FailingReactor
}

type testDriver struct {
	cluster1                  cluster
	cluster2                  cluster
	brokerDynClient           dynamic.Interface
	brokerServiceImportClient *fake.DynamicResourceClient
	brokerEndpointSliceClient *fake.DynamicResourceClient
	service                   *corev1.Service
	serviceExport             *mcsv1a1.ServiceExport
	endpoints                 *corev1.Endpoints
	stopCh                    chan struct{}
	restMapper                meta.RESTMapper
	syncerScheme              *runtime.Scheme
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
				Namespace: test.LocalNamespace,
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.253.9.1",
				Selector:  map[string]string{"app": "test"},
			},
		},
		restMapper: test.GetRESTMapperFor(&mcsv1a1.ServiceExport{}, &mcsv1a1.ServiceImport{},
			&corev1.Service{}, &corev1.Endpoints{}, &discovery.EndpointSlice{}),
		brokerDynClient: fake.NewDynamicClient(syncerScheme),
		syncerScheme:    syncerScheme,
		stopCh:          make(chan struct{}),
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

	t.brokerServiceImportClient = t.brokerDynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.RemoteNamespace).(*fake.DynamicResourceClient)

	t.brokerEndpointSliceClient = t.brokerDynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper,
		&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace).(*fake.DynamicResourceClient)

	t.cluster1.init(t.restMapper, syncerScheme)
	t.cluster2.init(t.restMapper, syncerScheme)

	return t
}

func (t *testDriver) justBeforeEach() {
	syncerConfig := &broker.SyncerConfig{
		BrokerNamespace: test.RemoteNamespace,
	}

	t.cluster1.start(t, syncerConfig, t.syncerScheme)
	t.cluster2.start(t, syncerConfig, t.syncerScheme)
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (c *cluster) init(restMapper meta.RESTMapper, syncerScheme *runtime.Scheme) {
	c.localDynClient = fake.NewDynamicClient(syncerScheme)

	c.localServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(restMapper,
		&mcsv1a1.ServiceExport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)

	c.localServiceImportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(restMapper,
		&mcsv1a1.ServiceImport{})).Namespace(test.LocalNamespace)

	c.localEndpointSliceClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(restMapper,
		&discovery.EndpointSlice{})).Namespace(test.LocalNamespace)

	fakeCS := fakeKubeClient.NewSimpleClientset()
	c.endpointsReactor = fake.NewFailingReactorForResource(&fakeCS.Fake, "endpoints")
	c.localKubeClient = fakeCS

	fake.AddDeleteCollectionReactor(&fakeCS.Fake, "EndpointSlice")
}

func (c *cluster) start(t *testDriver, syncerConfig *broker.SyncerConfig, syncerScheme *runtime.Scheme) {
	agentController, err := controller.NewWithDetail(&c.agentSpec, syncerConfig, t.restMapper, c.localDynClient, c.localKubeClient,
		syncerScheme, func(config *broker.SyncerConfig) (*broker.Syncer, error) {
			return broker.NewSyncerWithDetail(config, c.localDynClient, t.brokerDynClient, t.restMapper)
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
			APIVersion: "lighthouse.submariner.io.v2alpha1",
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
	t.awaitBrokerServiceImport(mcsv1a1.ClusterSetIP, serviceIP)
	t.cluster1.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)
	t.cluster2.awaitServiceImport(t.service, mcsv1a1.ClusterSetIP, serviceIP)

	t.awaitServiceExportStatus(statusIndex, newServiceExportCondition(mcsv1a1.ServiceExportValid,
		corev1.ConditionTrue, "AwaitingSync"), newServiceExportCondition(mcsv1a1.ServiceExportValid,
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
