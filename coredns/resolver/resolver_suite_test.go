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

package resolver_test

import (
	"flag"
	"fmt"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/lighthouse/coredns/constants"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	"github.com/submariner-io/lighthouse/coredns/resolver/fake"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	clusterID1          = "cluster1"
	clusterID2          = "cluster2"
	clusterID3          = "cluster3"
	service1            = "service1"
	namespace1          = "namespace1"
	namespace2          = "namespace2"
	submarinerNamespace = "submariner-operator"
	serviceIP1          = "192.168.56.21"
	serviceIP2          = "192.168.56.22"
	serviceIP3          = "192.168.56.23"
	endpointIP1         = "100.96.157.101"
	endpointIP2         = "100.96.157.102"
	endpointIP3         = "100.96.157.103"
	endpointIP4         = "100.96.157.104"
	endpointIP5         = "100.96.157.105"
	endpointIP6         = "100.96.157.106"
)

var (
	hostName1 = "host1"
	hostName2 = "host2"

	nodeName1 = "node1"
	nodeName2 = "node2"
	nodeName3 = "node3"

	ready    = true
	notReady = false

	port1 = mcsv1a1.ServicePort{
		Name:     "http",
		Protocol: corev1.ProtocolTCP,
		Port:     8080,
	}

	port2 = mcsv1a1.ServicePort{
		Name:     "POP3",
		Protocol: corev1.ProtocolUDP,
		Port:     110,
	}

	port3 = mcsv1a1.ServicePort{
		Name:     "https",
		Protocol: corev1.ProtocolTCP,
		Port:     443,
	}

	port4 = mcsv1a1.ServicePort{
		Name:     "SMTP",
		Protocol: corev1.ProtocolUDP,
		Port:     25,
	}
)

func init() {
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	_ = flags.Parse([]string{"-v=2"})
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

func TestResolver(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Resolver Suite")
}

type testDriver struct {
	clusterStatus  *fake.ClusterStatus
	resolver       *resolver.Interface
	endpointSlices dynamic.NamespaceableResourceInterface
	serviceImports dynamic.NamespaceableResourceInterface
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.clusterStatus = fake.NewClusterStatus("", clusterID1, clusterID2, clusterID3)

		Expect(discovery.AddToScheme(scheme.Scheme)).To(Succeed())
		Expect(mcsv1a1.AddToScheme(scheme.Scheme)).To(Succeed())

		client := fakeClient.NewSimpleDynamicClient(scheme.Scheme)

		restMapper := test.GetRESTMapperFor(&discovery.EndpointSlice{}, &mcsv1a1.ServiceImport{})

		t.endpointSlices = client.Resource(*test.GetGroupVersionResourceFor(restMapper, &discovery.EndpointSlice{}))
		t.serviceImports = client.Resource(*test.GetGroupVersionResourceFor(restMapper, &mcsv1a1.ServiceImport{}))

		t.resolver = resolver.New(t.clusterStatus, client)
		controller := resolver.NewController(t.resolver)

		Expect(controller.Start(watcher.Config{
			RestMapper: restMapper,
			Client:     client,
		})).To(Succeed())

		DeferCleanup(controller.Stop)
	})

	return t
}

func (t *testDriver) createServiceImport(si *mcsv1a1.ServiceImport) {
	test.CreateResource(t.serviceImports.Namespace(si.Namespace), si)
}

func (t *testDriver) createEndpointSlice(es *discovery.EndpointSlice) {
	test.CreateResource(t.endpointSlices.Namespace(es.Namespace), es)
}

func (t *testDriver) awaitDNSRecordsFound(ns, name, cluster, hostname string, expIsHeadless bool, expRecords ...resolver.DNSRecord) {
	var records []resolver.DNSRecord
	var found, isHeadless bool

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		records, isHeadless, found = t.resolver.GetDNSRecords(ns, name, cluster, hostname)
		return found && isHeadless == expIsHeadless && reflect.DeepEqual(records, expRecords), nil
	})
	if err == nil {
		return
	}

	Expect(found).To(BeTrue())
	Expect(isHeadless).To(Equal(expIsHeadless))

	t.assertDNSRecords(records, expRecords...)
}

func (t *testDriver) assertDNSRecordsFound(ns, name, cluster, hostname string, expIsHeadless bool, expRecords ...resolver.DNSRecord) {
	records, isHeadless, found := t.resolver.GetDNSRecords(ns, name, cluster, hostname)

	Expect(found).To(BeTrue())
	Expect(isHeadless).To(Equal(expIsHeadless))

	t.assertDNSRecords(records, expRecords...)
}

func (t *testDriver) assertDNSRecords(records []resolver.DNSRecord, expRecords ...resolver.DNSRecord) {
	recordFound := func(r resolver.DNSRecord) bool {
		for i := range expRecords {
			if reflect.DeepEqual(r, expRecords[i]) {
				return true
			}
		}

		return false
	}

	for i := range records {
		if !recordFound(records[i]) {
			Fail(fmt.Sprintf("Unexpected DNS record returned: %s\nExpected:\n%s",
				format.Object(records[i], 1), format.Object(expRecords, 1)))
		}
	}

	if len(records) != len(expRecords) {
		Fail(fmt.Sprintf("Expected %d DNS record returned, received %d.\nActual: %s\nExpected:\n%s",
			len(expRecords), len(records), format.Object(records, 1), format.Object(expRecords, 1)))
	}
}

func (t *testDriver) getNonHeadlessDNSRecord(ns, name, cluster string) *resolver.DNSRecord {
	records, isHeadless, found := t.resolver.GetDNSRecords(ns, name, cluster, "")

	Expect(found).To(BeTrue())
	Expect(isHeadless).To(BeFalse())
	Expect(records).To(HaveLen(1))

	return &records[0]
}

func (t *testDriver) assertDNSRecordsNotFound(ns, name, cluster, hostname string) {
	_, _, found := t.resolver.GetDNSRecords(ns, name, cluster, hostname)
	Expect(found).To(BeFalse())
}

func (t *testDriver) awaitDNSRecords(ns, name, cluster, hostname string, expFound bool) {
	Eventually(func() bool {
		_, _, found := t.resolver.GetDNSRecords(ns, name, cluster, hostname)
		return found
	}).Should(Equal(expFound))
}

func (t *testDriver) testRoundRobin(ns, service string, serviceIPs ...string) {
	ipsCount := len(serviceIPs)
	rrIPs := make([]string, 0)

	for i := 0; i < ipsCount; i++ {
		r := t.getNonHeadlessDNSRecord(ns, service, "")
		rrIPs = append(rrIPs, r.IP)
		slice := rrIPs[0:i]
		Expect(slice).ToNot(ContainElement(r.IP))
		Expect(serviceIPs).To(ContainElement(r.IP))
	}

	for i := 0; i < 5; i++ {
		for _, ip := range rrIPs {
			testIP := t.getNonHeadlessDNSRecord(ns, service, "").IP
			Expect(testIP).To(Equal(ip))
		}
	}
}

func (t *testDriver) putEndpointSlice(es *discovery.EndpointSlice) {
	Expect(t.resolver.PutEndpointSlice(es)).To(BeFalse())
}

func newClusterServiceImport(namespace, name, serviceIP, clusterID string, ports ...mcsv1a1.ServicePort) *mcsv1a1.ServiceImport {
	var ips []string
	if serviceIP != "" {
		ips = []string{serviceIP}
	}

	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: submarinerNamespace,
			Labels: map[string]string{
				mcsv1a1.LabelServiceName:        name,
				constants.LabelSourceNamespace:  namespace,
				constants.MCSLabelSourceCluster: clusterID,
			},
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  mcsv1a1.ClusterSetIP,
			IPs:   ips,
			Ports: ports,
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID,
				},
			},
		},
	}
}

func newClusterHeadlessServiceImport(namespace, name, clusterID string) *mcsv1a1.ServiceImport {
	si := newClusterServiceImport(namespace, name, "", clusterID)
	si.Spec.Type = mcsv1a1.Headless

	return si
}

func newClusterIPEndpointSlice(namespace, name, clusterID, clusterIP string, isHealthy bool,
	ports ...mcsv1a1.ServicePort) *discovery.EndpointSlice {
	if isHealthy {
		return newEndpointSlice(namespace, name, clusterID, ports, discovery.Endpoint{
			Addresses: []string{clusterIP},
		})
	}

	return newEndpointSlice(namespace, name, clusterID, ports)
}

func newEndpointSlice(namespace, name, clusterID string, ports []mcsv1a1.ServicePort,
	endpoints ...discovery.Endpoint) *discovery.EndpointSlice {
	epPorts := make([]discovery.EndpointPort, len(ports))
	for i := range ports {
		epPorts[i] = discovery.EndpointPort{
			Name:        &ports[i].Name,
			Protocol:    &ports[i].Protocol,
			Port:        &ports[i].Port,
			AppProtocol: ports[i].AppProtocol,
		}
	}

	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + namespace + "-" + clusterID,
			Namespace: namespace,
			Labels: map[string]string{
				discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				constants.LabelSourceNamespace:  namespace,
				constants.MCSLabelSourceCluster: clusterID,
				mcsv1a1.LabelServiceName:        name,
			},
		},
		AddressType: discovery.AddressTypeIPv4,
		Ports:       epPorts,
		Endpoints:   endpoints,
	}
}
