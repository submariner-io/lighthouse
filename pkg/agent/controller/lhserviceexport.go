package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newLHServiceExportController(localClient dynamic.Interface, restMapper meta.RESTMapper,
	scheme *runtime.Scheme) (*LHServiceExportController, error) {
	serviceExportController := LHServiceExportController{}

	lhServiceExportSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "lhServiceExport -> mcsServiceExport",
		SourceClient:    localClient,
		SourceNamespace: metav1.NamespaceAll,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       broker.NewFederator(localClient, restMapper, metav1.NamespaceAll, ""),
		ResourceType:    &lighthousev2a1.ServiceExport{},
		Transform:       serviceExportController.LHtoMCSServiceExport,
		Scheme:          scheme,
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
