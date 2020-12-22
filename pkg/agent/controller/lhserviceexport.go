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
package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newLHServiceExportController(localClient dynamic.Interface, restMapper meta.RESTMapper,
	scheme *runtime.Scheme) (*LHServiceExportController, error) {
	serviceExportController := LHServiceExportController{
		localClient: localClient,
	}

	lhServiceExportSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:             "lhServiceExport -> mcsServiceExport",
		SourceClient:     localClient,
		SourceNamespace:  metav1.NamespaceAll,
		Direction:        syncer.LocalToRemote,
		RestMapper:       restMapper,
		Federator:        broker.NewFederator(localClient, restMapper, metav1.NamespaceAll, ""),
		ResourceType:     &lighthousev2a1.ServiceExport{},
		Transform:        serviceExportController.LHtoMCSServiceExport,
		Scheme:           scheme,
		OnSuccessfulSync: serviceExportController.deleteLHServiceExport,
	})

	if err != nil {
		return nil, err
	}

	serviceExportController.lhServiceExportSyncer = lhServiceExportSyncer

	return &serviceExportController, nil
}

func (c *LHServiceExportController) start(stopCh <-chan struct{}) error {
	if err := c.lhServiceExportSyncer.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (c *LHServiceExportController) LHtoMCSServiceExport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	if op != syncer.Create {
		return nil, false
	}

	serviceExportCreated := obj.(*lighthousev2a1.ServiceExport)
	objMeta := serviceExportCreated.GetObjectMeta()
	mcsServiceExport := &mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objMeta.GetName(),
			Namespace: objMeta.GetNamespace(),
		},
	}

	return mcsServiceExport, false
}

func (c *LHServiceExportController) deleteLHServiceExport(obj runtime.Object, op syncer.Operation) {
	serviceExportDeleted := obj.(*mcsv1a1.ServiceExport)
	resourceClient := c.localClient.Resource(schema.GroupVersionResource{Group: "lighthouse.submariner.io",
		Version: "v2alpha1", Resource: "serviceexports"}).Namespace(serviceExportDeleted.ObjectMeta.GetNamespace())
	err := resourceClient.Delete(serviceExportDeleted.ObjectMeta.GetName(), &metav1.DeleteOptions{})

	if err != nil {
		klog.Errorf("Error deleting the ServiceExport %q: %v", serviceExportDeleted.GetObjectMeta().GetName(), err)
	}
}
