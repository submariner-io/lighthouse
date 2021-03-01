package controller

import (
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
	utilnet "k8s.io/utils/net"
)

func startEndpointController(localClient dynamic.Interface, restMapper meta.RESTMapper, scheme *runtime.Scheme,
	serviceImportUID types.UID, serviceImportName, serviceImportNameSpace, serviceName, clusterID string) (*EndpointController, error) {
	klog.V(log.DEBUG).Infof("Starting Endpoints controller for service %q", serviceName)

	controller := &EndpointController{
		clusterID:                    clusterID,
		serviceImportUID:             serviceImportUID,
		serviceImportName:            serviceImportName,
		serviceImportSourceNameSpace: serviceImportNameSpace,
		serviceName:                  serviceName,
		stopCh:                       make(chan struct{}),
	}

	nameSelector := fields.OneTermEqualSelector("metadata.name", serviceName)

	epsSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Endpoints -> EndpointSlice",
		SourceClient:        localClient,
		SourceNamespace:     serviceImportNameSpace,
		SourceFieldSelector: nameSelector.String(),
		Direction:           syncer.LocalToRemote,
		RestMapper:          restMapper,
		Federator:           broker.NewFederator(localClient, restMapper, serviceImportNameSpace, "", "ownerReferences"),
		ResourceType:        &corev1.Endpoints{},
		Transform:           controller.endpointsToEndpointSlice,
		Scheme:              scheme,
	})
	if err != nil {
		return nil, err
	}

	if err := epsSyncer.Start(controller.stopCh); err != nil {
		return nil, err
	}

	controller.localClient = localClient

	return controller, nil
}

func (e *EndpointController) stop() {
	close(e.stopCh)
	e.cleanup()
}

func (e *EndpointController) cleanup() {
	resourceClient := e.localClient.Resource(schema.GroupVersionResource{Group: "discovery.k8s.io",
		Version: "v1beta1", Resource: "endpointslices"}).Namespace(e.serviceImportSourceNameSpace)

	endpointSliceLabels := labels.SelectorFromSet(map[string]string{lhconstants.LabelServiceImportName: e.serviceImportName})
	listEndpointSliceOptions := metav1.ListOptions{
		LabelSelector: endpointSliceLabels.String(),
	}

	err := resourceClient.DeleteCollection(&metav1.DeleteOptions{}, listEndpointSliceOptions)

	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("Error deleting the EndpointSlices associated with serviceImport %q: %v", e.serviceImportName, err)
	}
}

func (e *EndpointController) endpointsToEndpointSlice(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	endPoints := obj.(*corev1.Endpoints)
	endpointSliceName := endPoints.Name + "-" + e.clusterID

	if op == syncer.Delete {
		return &discovery.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      endpointSliceName,
				Namespace: endPoints.Namespace,
			},
		}, false
	}

	return e.endpointSliceFromEndpoints(endPoints), false
}

func (e *EndpointController) endpointSliceFromEndpoints(endpoints *corev1.Endpoints) *discovery.EndpointSlice {
	endpointSlice := &discovery.EndpointSlice{}
	controllerFlag := false
	endpointSlice.Name = endpoints.Name + "-" + e.clusterID
	endpointSlice.Labels = map[string]string{
		lhconstants.LabelServiceImportName: e.serviceImportName,
		discovery.LabelManagedBy:           lhconstants.LabelValueManagedBy,
		lhconstants.LabelSourceNamespace:   e.serviceImportSourceNameSpace,
		lhconstants.LabelSourceCluster:     e.clusterID,
		lhconstants.LabelSourceName:        e.serviceName,
	}
	endpointSlice.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         "multicluster.x-k8s.io.v1alpha1",
		Kind:               "ServiceImport",
		Name:               e.serviceImportName,
		UID:                e.serviceImportUID,
		Controller:         &controllerFlag,
		BlockOwnerDeletion: nil,
	}}

	endpointSlice.AddressType = discovery.AddressTypeIPv4

	if len(endpoints.Subsets) > 0 {
		subset := endpoints.Subsets[0]
		for i := range subset.Ports {
			endpointSlice.Ports = append(endpointSlice.Ports, discovery.EndpointPort{
				Port:     &subset.Ports[i].Port,
				Name:     &subset.Ports[i].Name,
				Protocol: &subset.Ports[i].Protocol,
			})
		}

		if allAddressesIPv6(append(subset.Addresses, subset.NotReadyAddresses...)) {
			endpointSlice.AddressType = discovery.AddressTypeIPv6
		}

		endpointSlice.Endpoints = append(endpointSlice.Endpoints, getEndpointsFromAddresses(subset.Addresses, endpointSlice.AddressType, true)...)
		endpointSlice.Endpoints = append(endpointSlice.Endpoints, getEndpointsFromAddresses(subset.NotReadyAddresses,
			endpointSlice.AddressType, false)...)
	}

	return endpointSlice
}

func getEndpointsFromAddresses(addresses []corev1.EndpointAddress, addressType discovery.AddressType, ready bool) []discovery.Endpoint {
	endpoints := []discovery.Endpoint{}
	isIPv6AddressType := addressType == discovery.AddressTypeIPv6

	for _, address := range addresses {
		if utilnet.IsIPv6String(address.IP) == isIPv6AddressType {
			endpoints = append(endpoints, endpointFromAddress(address, ready))
		}
	}

	return endpoints
}

func endpointFromAddress(address corev1.EndpointAddress, ready bool) discovery.Endpoint {
	topology := map[string]string{}
	if address.NodeName != nil {
		topology["kubernetes.io/hostname"] = *address.NodeName
	}

	endpoint := discovery.Endpoint{
		Addresses:  []string{address.IP},
		Conditions: discovery.EndpointConditions{Ready: &ready},
		Topology:   topology,
	}

	/*
		We only need TargetRef.Name as pod address and hostname are only relevant fields.
		Avoid copying TargetRef coz it it has revision which can change for reasons other
		than address and hostname, resulting in unnecessary syncs across clusters.
		Revisit this logic if we need TargetRef for other use cases.
	*/

	switch {
	case address.Hostname != "":
		endpoint.Hostname = &address.Hostname
	case address.TargetRef != nil:
		endpoint.Hostname = &address.TargetRef.Name
	}

	return endpoint
}

func allAddressesIPv6(addresses []corev1.EndpointAddress) bool {
	if len(addresses) == 0 {
		return false
	}

	for _, address := range addresses {
		if !utilnet.IsIPv6String(address.IP) {
			return false
		}
	}

	return true
}
