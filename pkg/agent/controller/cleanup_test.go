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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Cleanup", func() {
	var (
		t                                          *testDriver
		localServiceImport1                        *mcsv1a1.ServiceImport
		localAggregatedServiceImport1              *mcsv1a1.ServiceImport
		aggregatedServiceImportOnRemoteBroker1     *mcsv1a1.ServiceImport
		localServiceImport2                        *mcsv1a1.ServiceImport
		localAggregatedServiceImport2              *mcsv1a1.ServiceImport
		aggregatedServiceImportOnRemoteBroker2     *mcsv1a1.ServiceImport
		remoteAggregatedServiceImportOnLocalBroker *mcsv1a1.ServiceImport
		localEndpointSlice                         *discovery.EndpointSlice
		remoteEndpointSliceOnLocalBroker           *discovery.EndpointSlice
		remoteEndpointSliceInLocalNS               *discovery.EndpointSlice
		nonLHEndpointSlice                         *discovery.EndpointSlice
		localBrokerServiceImportClient             dynamic.ResourceInterface
		localBrokerEndpointSliceClient             dynamic.ResourceInterface
	)

	BeforeEach(func() {
		t = newTestDiver()
		t.doStart = false
	})

	JustBeforeEach(func() {
		t.justBeforeEach()

		// Exported ServiceImport in one cluster

		localServiceImport1 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s-%s", serviceName, serviceNamespace, clusterID1),
				Labels: map[string]string{
					mcsv1a1.LabelServiceName:        serviceName,
					constants.LabelSourceNamespace:  serviceNamespace,
					constants.MCSLabelSourceCluster: clusterID1,
				},
			},
		}

		test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport1)

		localAggregatedServiceImport1 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: serviceName,
			},
			Status: mcsv1a1.ServiceImportStatus{Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID1,
				},
			}},
		}

		test.CreateResource(t.cluster1.localServiceImportClient.Namespace(serviceNamespace), localAggregatedServiceImport1)

		aggregatedServiceImportOnRemoteBroker1 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace),
				Annotations: map[string]string{
					mcsv1a1.LabelServiceName:       serviceName,
					constants.LabelSourceNamespace: serviceNamespace,
				},
			},
		}
		aggregatedServiceImportOnRemoteBroker1.Status = localAggregatedServiceImport1.Status

		test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImportOnRemoteBroker1)

		// Exported ServiceImport in two clusters

		serviceNamespace2 := "ns2"

		localServiceImport2 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s-%s", serviceName, serviceNamespace2, clusterID1),
				Labels: map[string]string{
					mcsv1a1.LabelServiceName:        serviceName,
					constants.LabelSourceNamespace:  serviceNamespace2,
					constants.MCSLabelSourceCluster: clusterID1,
				},
			},
		}

		test.CreateResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport2)

		localAggregatedServiceImport2 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: serviceName,
			},
			Status: mcsv1a1.ServiceImportStatus{Clusters: []mcsv1a1.ClusterStatus{
				{
					Cluster: clusterID1,
				},
				{
					Cluster: clusterID2,
				},
			}},
		}

		test.CreateResource(t.cluster1.localServiceImportClient.Namespace(serviceNamespace2), localAggregatedServiceImport2)

		aggregatedServiceImportOnRemoteBroker2 = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace2),
				Annotations: map[string]string{
					mcsv1a1.LabelServiceName:       serviceName,
					constants.LabelSourceNamespace: serviceNamespace2,
				},
			},
		}
		aggregatedServiceImportOnRemoteBroker2.Status = localAggregatedServiceImport2.Status

		test.CreateResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImportOnRemoteBroker2)

		// Remote ServiceImport in local broker

		serviceNamespace3 := "ns3"

		localBrokerServiceImportClient = t.cluster1.localDynClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
			&mcsv1a1.ServiceImport{})).Namespace(test.RemoteNamespace)

		remoteAggregatedServiceImportOnLocalBroker = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", serviceName, serviceNamespace3),
				Annotations: map[string]string{
					mcsv1a1.LabelServiceName:       serviceName,
					constants.LabelSourceNamespace: serviceNamespace3,
				},
			},
		}

		test.CreateResource(localBrokerServiceImportClient, remoteAggregatedServiceImportOnLocalBroker)

		// Local EndpointSlice in remote broker

		localEndpointSlice = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx-ns1" + clusterID1,
				Labels: map[string]string{
					constants.MCSLabelSourceCluster: clusterID1,
					discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, localEndpointSlice)
		test.CreateResource(t.brokerEndpointSliceClient, test.SetClusterIDLabel(localEndpointSlice, clusterID1))

		// Remote EndpointSlice in local broker

		localBrokerEndpointSliceClient = t.cluster1.localDynClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
			&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace)

		remoteEndpointSliceOnLocalBroker = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx-ns3" + clusterID2,
				Labels: map[string]string{
					constants.MCSLabelSourceCluster: clusterID2,
					discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(localBrokerEndpointSliceClient, test.SetClusterIDLabel(remoteEndpointSliceOnLocalBroker, clusterID2))

		// Remote EndpointSlice in local namespace

		remoteEndpointSliceInLocalNS = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx2-" + clusterID2,
				Labels: map[string]string{
					constants.MCSLabelSourceCluster: clusterID2,
					discovery.LabelManagedBy:        constants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, remoteEndpointSliceInLocalNS)

		nonLHEndpointSlice = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other",
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, nonLHEndpointSlice)
	})

	AfterEach(func() {
		t.afterEach()
	})

	It("should correctly remove local and remote ServiceImports and EndpointSlices", func() {
		Expect(t.cluster1.agentController.Cleanup(context.Background())).To(Succeed())

		test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport1.Name)
		test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(serviceNamespace), localAggregatedServiceImport1.Name)
		test.AwaitNoResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImportOnRemoteBroker1.Name)

		test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(test.LocalNamespace), localServiceImport2.Name)
		test.AwaitNoResource(t.cluster1.localServiceImportClient.Namespace(serviceNamespace), localAggregatedServiceImport2.Name)

		si := ensureResource(t.brokerServiceImportClient.Namespace(test.RemoteNamespace), aggregatedServiceImportOnRemoteBroker2.Name,
			&mcsv1a1.ServiceImport{}).(*mcsv1a1.ServiceImport)
		Expect(si.Status.Clusters).To(HaveLen(1))
		Expect(si.Status.Clusters).To(ContainElement(mcsv1a1.ClusterStatus{Cluster: clusterID2}))

		ensureResource(localBrokerServiceImportClient, remoteAggregatedServiceImportOnLocalBroker.Name, &mcsv1a1.ServiceImport{})

		test.AwaitNoResource(t.brokerEndpointSliceClient, localEndpointSlice.GetName())
		test.AwaitNoResource(t.cluster1.localEndpointSliceClient, remoteEndpointSliceInLocalNS.Name)

		ensureResource(localBrokerEndpointSliceClient, remoteEndpointSliceOnLocalBroker.Name, &discovery.EndpointSlice{})
		ensureResource(t.cluster1.localEndpointSliceClient, nonLHEndpointSlice.Name, &discovery.EndpointSlice{})
	})
})

func ensureResource(client dynamic.ResourceInterface, name string, to runtime.Object) runtime.Object {
	Consistently(func() bool {
		_, err := client.Get(context.TODO(), name, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}).Should(BeFalse(), "Expected resource %q not found", name)

	obj, err := client.Get(context.TODO(), name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	utilruntime.Must(scheme.Scheme.Convert(obj, to, nil))

	return to
}
