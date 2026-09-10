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

package controller

import (
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/admiral/pkg/workqueue"
	"github.com/submariner-io/lighthouse/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	validation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
)

const (
	// ReflectorManagedByLabel/Value mark the cluster.local Service and EndpointSlice
	// objects the reflector owns. Only objects carrying this label are ever created,
	// updated or deleted by it.
	ReflectorManagedByLabel = "app.kubernetes.io/managed-by"
	ReflectorManagedByValue = "lighthouse-clusterlocal-reflector"

	// reflectedFromLabel records the imported EndpointSlice a reflected EndpointSlice
	// was derived from (1:1). It is also the federator's identifying label, so the
	// reflected slice (created with GenerateName) is matched on update and delete.
	reflectedFromLabel = "lighthouse.submariner.io/reflected-from"
)

// ServiceGVR is the GroupVersionResource of the Services the reflector creates.
// (endpointSliceGVR is declared in cleanup.go.)
var ServiceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

// ClusterLocalReflector reflects each service imported from another cluster into a
// native cluster.local Service + EndpointSlice named "<service>-<sourceCluster>" in
// the service's namespace, so workloads can resolve cross-cluster services through
// the standard cluster DNS path. This is for clusters whose DNS cannot host the
// clusterset.local forward stanza (e.g. EKS Auto Mode's immutable node-local
// CoreDNS). It is instantiated only when AgentSpecification.ReflectClusterLocal is
// set; default behaviour (clusterset.local only) is unchanged.
//
// It consumes the imported EndpointSlices the agent already syncs to the local
// service namespace (labelled discovery.LabelManagedBy=LabelValueManagedBy, with
// LabelServiceName/LabelSourceCluster/LabelSourceNamespace), reflecting only those
// whose source cluster is not this cluster.
type ClusterLocalReflector struct {
	clusterID           string
	syncer              syncer.Interface
	serviceWatcher      watcher.Interface
	federator           federate.Federator
	serviceClient       dynamic.NamespaceableResourceInterface
	endpointSliceClient dynamic.NamespaceableResourceInterface
	namespaceValidator  *NamespaceValidator
}

//nolint:gocritic // (hugeParam) matches the other controller constructors.
func newClusterLocalReflector(spec *AgentSpecification, syncerConfig broker.SyncerConfig, namespaceValidator *NamespaceValidator,
) (*ClusterLocalReflector, error) {
	c := &ClusterLocalReflector{
		clusterID:           spec.ClusterID,
		serviceClient:       syncerConfig.LocalClient.Resource(ServiceGVR),
		endpointSliceClient: syncerConfig.LocalClient.Resource(endpointSliceGVR),
		namespaceValidator:  namespaceValidator,
	}

	// Empty TargetNamespace => the federator uses each object's own namespace.
	// IdentifyingLabels let the federator find the reflected EndpointSlice (which
	// uses GenerateName) on update and delete; the deterministically-named Service
	// is matched by name and ignores them.
	c.federator = federate.NewCreateOrUpdateFederator(federate.CreateOrUpdateOptions{
		Client:            syncerConfig.LocalClient,
		RestMapper:        syncerConfig.RestMapper,
		TargetNamespace:   metav1.NamespaceAll,
		IdentifyingLabels: []string{ReflectorManagedByLabel, reflectedFromLabel},
	})

	var err error

	c.syncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ClusterLocal reflector",
		SourceClient:    syncerConfig.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		SourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
			discovery.LabelManagedBy: constants.LabelValueManagedBy,
		}).String(),
		RestMapper:      syncerConfig.RestMapper,
		Federator:       c.federator,
		ResourceType:    &discovery.EndpointSlice{},
		Transform:       c.reflect,
		Scheme:          syncerConfig.Scheme,
		WorkQueueConfig: workqueue.ConfigFromGlobal("clusterlocal-reflector", nil),
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating ClusterLocal reflector syncer")
	}

	// Watch the reflected Services so that, when one is deleted (or edited so it no
	// longer carries our managed-by label and thus drops out of this watch), the
	// now-orphaned reflected EndpointSlices are cleaned up. The EndpointSlice syncer
	// alone wouldn't notice this, as it only watches imported EndpointSlices.
	c.serviceWatcher, err = watcher.New(&watcher.Config{
		Client:     syncerConfig.LocalClient,
		RestMapper: syncerConfig.RestMapper,
		Scheme:     syncerConfig.Scheme,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:            "ClusterLocal reflected Service",
				ResourceType:    &corev1.Service{},
				SourceNamespace: metav1.NamespaceAll,
				SourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
					ReflectorManagedByLabel: ReflectorManagedByValue,
				}).String(),
				Handler: watcher.EventHandlerFuncs{
					OnDeleteFunc: c.onReflectedServiceDeleted,
				},
			},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating ClusterLocal reflected Service watcher")
	}

	return c, nil
}

func (c *ClusterLocalReflector) start(stopCh <-chan struct{}) error {
	if err := c.syncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ClusterLocal reflector syncer")
	}

	if err := c.serviceWatcher.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting ClusterLocal reflected Service watcher")
	}

	return nil
}

// onReflectedServiceDeleted deletes the reflected EndpointSlices belonging to a
// reflected Service that was deleted or edited out of reflector ownership.
func (c *ClusterLocalReflector) onReflectedServiceDeleted(obj runtime.Object, _ int) bool {
	svc := obj.(*corev1.Service)

	selector := k8slabels.SelectorFromSet(map[string]string{
		discovery.LabelServiceName: svc.Name,
		ReflectorManagedByLabel:    ReflectorManagedByValue,
	}).String()

	err := c.endpointSliceClient.Namespace(svc.Namespace).DeleteCollection(context.TODO(),
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting reflected EndpointSlices for Service %s/%s", svc.Namespace, svc.Name)
		return true
	}

	logger.Infof("Cleaned up reflected EndpointSlices for removed Service %s/%s", svc.Namespace, svc.Name)

	return false
}

// reflect is the syncer Transform. The returned EndpointSlice is created/updated/
// deleted by the syncer's federator; the matching Service is reconciled here.
func (c *ClusterLocalReflector) reflect(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	imported := obj.(*discovery.EndpointSlice)

	serviceName := imported.Labels[mcsv1b1.LabelServiceName]
	sourceCluster := imported.Labels[mcsv1b1.LabelSourceCluster]
	serviceNamespace := imported.Labels[constants.LabelSourceNamespace]

	// Reflect only services imported from *other* clusters. Our own exported
	// services already exist locally under their real cluster.local name.
	if serviceName == "" || serviceNamespace == "" || sourceCluster == "" || sourceCluster == c.clusterID {
		return nil, false
	}

	if op != syncer.Delete {
		if err := c.namespaceValidator.CheckAllowed(serviceNamespace); err != nil {
			logger.Warningf("Rejecting EndpointSlice from cluster %q: %v", imported.Labels[mcsv1b1.LabelSourceCluster], err)

			return nil, false
		}
	}

	reflectedName := serviceName + "-" + sourceCluster

	// The reflected name must be a valid Service name (DNS-1123 label, <=63 chars).
	// An over-long service/cluster combination can't be reflected; skip and log
	// rather than failing on every reconcile.
	if errs := validation.IsDNS1123Label(reflectedName); len(errs) > 0 {
		logger.Warningf("Cannot reflect imported service %q from cluster %q: %q is not a valid Service name: %v",
			serviceName, sourceCluster, reflectedName, errs)

		return nil, false
	}

	reflectedSlice := c.newReflectedSlice(imported, reflectedName, serviceNamespace)
	ctx := context.TODO()

	if op == syncer.Delete {
		// The syncer deletes the returned slice. Delete the Service too once no
		// imported slices remain for this (service, source cluster).
		if !c.hasOtherImportedSlices(serviceName, sourceCluster, serviceNamespace, imported.Name) {
			if err := c.federator.Delete(ctx, c.newReflectedService(imported, reflectedName, serviceNamespace)); err != nil {
				logger.Errorf(err, "Error deleting reflected Service %s/%s", serviceNamespace, reflectedName)
				return reflectedSlice, true
			}
		}

		return reflectedSlice, false
	}

	// Never take over a Service we don't own (collision with a real local service).
	owned, err := c.serviceIsReflectable(ctx, serviceNamespace, reflectedName)
	if err != nil {
		logger.Errorf(err, "Error checking reflected Service %s/%s", serviceNamespace, reflectedName)
		return nil, true
	}

	if !owned {
		logger.Warningf("Service %s/%s already exists and is not reflector-owned; skipping reflection of imported "+
			"service %q from cluster %q", serviceNamespace, reflectedName, serviceName, sourceCluster)

		return nil, false
	}

	if err := c.federator.Distribute(ctx, c.newReflectedService(imported, reflectedName, serviceNamespace)); err != nil {
		logger.Errorf(err, "Error creating reflected Service %s/%s", serviceNamespace, reflectedName)
		return nil, true
	}

	return reflectedSlice, false
}

func (c *ClusterLocalReflector) newReflectedService(imported *discovery.EndpointSlice, name, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				ReflectorManagedByLabel:    ReflectorManagedByValue,
				mcsv1b1.LabelServiceName:   imported.Labels[mcsv1b1.LabelServiceName],
				mcsv1b1.LabelSourceCluster: imported.Labels[mcsv1b1.LabelSourceCluster],
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone, // headless: DNS returns the reflected endpoint addresses
			Ports:     servicePortsFrom(imported.Ports),
		},
	}
}

func (c *ClusterLocalReflector) newReflectedSlice(imported *discovery.EndpointSlice, serviceName, namespace string,
) *discovery.EndpointSlice {
	return &discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			// GenerateName (vs a deterministic name) avoids any length/collision
			// concern; the federator finds it again via the identifying labels.
			GenerateName: serviceName + "-",
			Namespace:    namespace,
			Labels: map[string]string{
				// Associates this slice with the reflected Service for kube DNS/proxy.
				discovery.LabelServiceName: serviceName,
				ReflectorManagedByLabel:    ReflectorManagedByValue,
				reflectedFromLabel:         imported.Name,
			},
		},
		AddressType: imported.AddressType,
		Ports:       imported.Ports,
		Endpoints:   imported.Endpoints,
	}
}

// serviceIsReflectable reports whether the reflector may create/update the named
// Service: true if it doesn't exist or is already reflector-owned, false if a
// foreign Service of that name exists (collision).
func (c *ClusterLocalReflector) serviceIsReflectable(ctx context.Context, namespace, name string) (bool, error) {
	existing, err := c.serviceClient.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}

	if err != nil {
		return false, errors.Wrapf(err, "error retrieving Service %s/%s", namespace, name)
	}

	return existing.GetLabels()[ReflectorManagedByLabel] == ReflectorManagedByValue, nil
}

func (c *ClusterLocalReflector) hasOtherImportedSlices(serviceName, sourceCluster, namespace, excludeName string) bool {
	selector := k8slabels.SelectorFromSet(map[string]string{
		discovery.LabelManagedBy:   constants.LabelValueManagedBy,
		mcsv1b1.LabelServiceName:   serviceName,
		mcsv1b1.LabelSourceCluster: sourceCluster,
	})

	for _, o := range c.syncer.ListResourcesBySelector(selector) {
		s := o.(*discovery.EndpointSlice)
		if s.Namespace == namespace && s.Name != excludeName {
			return true
		}
	}

	return false
}

func servicePortsFrom(in []discovery.EndpointPort) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, 0, len(in))

	for i := range in {
		p := &in[i]
		ports = append(ports, corev1.ServicePort{
			Protocol:    ptr.Deref(p.Protocol, corev1.ProtocolTCP),
			Name:        ptr.Deref(p.Name, ""),
			Port:        ptr.Deref(p.Port, 0),
			TargetPort:  intstr.FromInt32(ptr.Deref(p.Port, 0)),
			AppProtocol: p.AppProtocol,
		})
	}

	return ports
}
