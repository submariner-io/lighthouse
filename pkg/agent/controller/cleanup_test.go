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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("Cleanup", func() {
	var (
		t                                    *testDriver
		existingLocalServiceImport           *mcsv1a1.ServiceImport
		existingRemoteServiceImport          *mcsv1a1.ServiceImport
		existingLocalServiceImportInRemoteNS *mcsv1a1.ServiceImport
		existingLocalEndpointSlice           *discovery.EndpointSlice
		existingLocalEndpointSliceInRemoteNS *discovery.EndpointSlice
		existingRemoteEndpointSlice          *discovery.EndpointSlice
		nonLHEndpointSlice                   *discovery.EndpointSlice
		remoteNSServiceImportClient          dynamic.ResourceInterface
		remoteNSEndpointSliceClient          dynamic.ResourceInterface
	)

	BeforeEach(func() {
		t = newTestDiver()
		t.doStart = false
	})

	JustBeforeEach(func() {
		t.justBeforeEach()

		existingLocalServiceImport = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx-" + serviceNamespace + "-" + clusterID1,
				Labels: map[string]string{
					lhconstants.LighthouseLabelSourceCluster: clusterID1,
				},
			},
		}

		test.CreateResource(t.cluster1.localServiceImportClient, existingLocalServiceImport)
		test.CreateResource(t.brokerServiceImportClient, test.SetClusterIDLabel(existingLocalServiceImport, clusterID1))

		remoteNSServiceImportClient = t.cluster1.localDynClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
			&mcsv1a1.ServiceImport{})).Namespace(test.RemoteNamespace)

		existingLocalServiceImportInRemoteNS = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-" + serviceNamespace + "-" + clusterID2,
				Labels: map[string]string{
					lhconstants.LighthouseLabelSourceCluster: clusterID2,
				},
			},
		}

		test.CreateResource(remoteNSServiceImportClient, test.SetClusterIDLabel(existingLocalServiceImportInRemoteNS, clusterID2))

		existingRemoteServiceImport = &mcsv1a1.ServiceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx2-" + serviceNamespace + "-" + clusterID2,
				Labels: map[string]string{
					lhconstants.LighthouseLabelSourceCluster: clusterID2,
				},
			},
		}

		test.CreateResource(t.cluster1.localServiceImportClient, test.SetClusterIDLabel(existingRemoteServiceImport, clusterID2))
		test.CreateResource(t.brokerServiceImportClient, test.SetClusterIDLabel(existingRemoteServiceImport, clusterID2))

		existingLocalEndpointSlice = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx-" + clusterID1,
				Labels: map[string]string{
					lhconstants.MCSLabelSourceCluster: clusterID1,
					discovery.LabelManagedBy:          lhconstants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, existingLocalEndpointSlice)
		test.CreateResource(t.brokerEndpointSliceClient, test.SetClusterIDLabel(existingLocalEndpointSlice, clusterID1))

		remoteNSEndpointSliceClient = t.cluster1.localDynClient.Resource(*test.GetGroupVersionResourceFor(t.syncerConfig.RestMapper,
			&discovery.EndpointSlice{})).Namespace(test.RemoteNamespace)

		existingLocalEndpointSliceInRemoteNS = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-" + clusterID2,
				Labels: map[string]string{
					lhconstants.MCSLabelSourceCluster: clusterID2,
					discovery.LabelManagedBy:          lhconstants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(remoteNSEndpointSliceClient, test.SetClusterIDLabel(existingLocalEndpointSliceInRemoteNS, clusterID2))

		existingRemoteEndpointSlice = &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nginx2-" + clusterID2,
				Labels: map[string]string{
					lhconstants.MCSLabelSourceCluster: clusterID2,
					discovery.LabelManagedBy:          lhconstants.LabelValueManagedBy,
				},
			},
		}

		test.CreateResource(t.cluster1.localEndpointSliceClient, test.SetClusterIDLabel(existingRemoteEndpointSlice, clusterID2))
		test.CreateResource(t.brokerEndpointSliceClient, test.SetClusterIDLabel(existingRemoteEndpointSlice, clusterID2))

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

	It("should remove local LH ServiceImports and EndpointSlices from the remote datastore", func() {
		Expect(t.cluster1.agentController.Cleanup()).To(Succeed())

		test.AwaitNoResource(t.brokerServiceImportClient, existingLocalServiceImport.GetName())
		test.AwaitNoResource(t.brokerEndpointSliceClient, existingLocalEndpointSlice.GetName())

		time.Sleep(300 * time.Millisecond)
		test.AwaitResource(t.brokerServiceImportClient, existingRemoteServiceImport.GetName())
		test.AwaitResource(t.brokerEndpointSliceClient, existingRemoteEndpointSlice.GetName())
	})

	It("should remove all LH ServiceImports and EndpointSlices from the local datastore", func() {
		Expect(t.cluster1.agentController.Cleanup()).To(Succeed())

		test.AwaitNoResource(t.cluster1.localServiceImportClient, existingLocalServiceImport.GetName())
		test.AwaitNoResource(t.cluster1.localServiceImportClient, existingRemoteServiceImport.GetName())
		test.AwaitNoResource(t.cluster1.localEndpointSliceClient, existingLocalEndpointSlice.GetName())
		test.AwaitNoResource(t.cluster1.localEndpointSliceClient, existingRemoteEndpointSlice.GetName())

		time.Sleep(300 * time.Millisecond)
		test.AwaitResource(t.cluster1.localEndpointSliceClient, nonLHEndpointSlice.GetName())
		test.AwaitResource(remoteNSServiceImportClient, existingLocalServiceImportInRemoteNS.GetName())
		test.AwaitResource(remoteNSEndpointSliceClient, existingLocalEndpointSliceInRemoteNS.GetName())
	})
})
