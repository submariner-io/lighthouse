package controller

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type cluster struct {
	localDynClient              dynamic.Interface
	localLhServiceExportClient  *fake.DynamicResourceClient
	localMcsServiceExportClient *fake.DynamicResourceClient
}

type testDriver struct {
	cluster1        cluster
	brokerDynClient dynamic.Interface
	serviceExport   *lighthousev2a1.ServiceExport
	stopCh          chan struct{}
	restMapper      meta.RESTMapper
	syncerScheme    *runtime.Scheme
}

func (t *testDriver) justBeforeEach() {
	syncerConfig := &broker.SyncerConfig{
		BrokerNamespace: test.RemoteNamespace,
	}

	t.cluster1.start(t, syncerConfig, t.syncerScheme)
}

func (t *testDriver) afterEach() {
	close(t.stopCh)
}

func (t *testDriver) createServiceExport() {
	test.CreateResource(t.cluster1.localLhServiceExportClient, t.serviceExport)
}

func (c *cluster) start(t *testDriver, syncerConfig *broker.SyncerConfig, syncerScheme *runtime.Scheme) {
	lhExportController, err := newLHServiceExportController(c.localDynClient, t.restMapper, syncerScheme)

	Expect(err).To(Succeed())
	Expect(lhExportController.start(t.stopCh)).To(Succeed())
}

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
				t.createServiceExport()
				t.awaitNoLHServiceexport(t.cluster1.localLhServiceExportClient)
				t.awaitMcsService(t.cluster1.localMcsServiceExportClient, t.serviceExport)
			})
		})
	})
})

func newTestDiver() *testDriver {
	syncerScheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(syncerScheme)).To(Succeed())
	Expect(discovery.AddToScheme(syncerScheme)).To(Succeed())
	Expect(mcsv1a1.AddToScheme(syncerScheme)).To(Succeed())
	Expect(lighthousev2a1.AddToScheme(syncerScheme)).To(Succeed())

	t := &testDriver{
		cluster1:        cluster{},
		restMapper:      test.GetRESTMapperFor(&mcsv1a1.ServiceExport{}, &lighthousev2a1.ServiceExport{}),
		brokerDynClient: fake.NewDynamicClient(syncerScheme),
		syncerScheme:    syncerScheme,
		stopCh:          make(chan struct{}),
	}

	t.serviceExport = &lighthousev2a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service1",
			Namespace: test.LocalNamespace,
		},
	}

	t.cluster1.init(t.restMapper, syncerScheme)

	return t
}

func (c *cluster) init(restMapper meta.RESTMapper, syncerScheme *runtime.Scheme) {
	c.localDynClient = fake.NewDynamicClient(syncerScheme)

	c.localLhServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(restMapper,
		&lighthousev2a1.ServiceExport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)

	c.localMcsServiceExportClient = c.localDynClient.Resource(*test.GetGroupVersionResourceFor(restMapper,
		&mcsv1a1.ServiceExport{})).Namespace(test.LocalNamespace).(*fake.DynamicResourceClient)
}

func (t *testDriver) awaitMcsService(client dynamic.ResourceInterface, serviceExport *lighthousev2a1.ServiceExport) *mcsv1a1.ServiceExport {
	obj := test.AwaitResource(client, serviceExport.Name)

	mcsServiceExport := &mcsv1a1.ServiceExport{}
	Expect(scheme.Scheme.Convert(obj, mcsServiceExport, nil)).To(Succeed())

	Expect(mcsServiceExport.GetObjectMeta().GetName()).To(Equal(serviceExport.GetObjectMeta().GetName()))
	Expect(mcsServiceExport.GetObjectMeta().GetNamespace()).To(Equal(serviceExport.GetObjectMeta().GetNamespace()))

	return mcsServiceExport
}

func (t *testDriver) awaitNoLHServiceexport(client dynamic.ResourceInterface) {
	test.AwaitNoResource(client, "service1")
}
