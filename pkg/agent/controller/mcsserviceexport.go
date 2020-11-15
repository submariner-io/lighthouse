package controller

import (
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func newMCSServiceExportController(localClient dynamic.Interface, lighthouseClient lighthouseClientset.Interface,
	restMapper meta.RESTMapper, scheme *runtime.Scheme) (*MCSServiceExportController, error) {
	serviceExportController := MCSServiceExportController{
		lighthouseClient: lighthouseClient,
	}
	mcsServiceExportSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "mcsServiceExport -> lhServiceExport",
		SourceClient:    localClient,
		SourceNamespace: metav1.NamespaceAll,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       broker.NewFederator(localClient, restMapper, metav1.NamespaceAll, ""),
		ResourceType:    &mcsv1a1.ServiceExport{},
		Transform:       serviceExportController.MCStoLHServiceExport,
		Scheme:          scheme,
	})
	if err != nil {
		return nil, err
	}

	serviceExportController.mcsServiceExportSyncer = mcsServiceExportSyncer

	return &serviceExportController, nil
}

func (c *MCSServiceExportController) start(stopCh <-chan struct{}) error {
	if err := c.mcsServiceExportSyncer.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (c *MCSServiceExportController) MCStoLHServiceExport(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	if op != syncer.Update {
		return nil, false
	}

	serviceExportCreated := obj.(*mcsv1a1.ServiceExport)

	objMeta := serviceExportCreated.GetObjectMeta()
	_, err := c.lighthouseClient.LighthouseV2alpha1().ServiceExports(objMeta.GetNamespace()).
		Get(objMeta.GetName(), metav1.GetOptions{})

	if errors.IsNotFound(err) {
		return nil, false
	} else if err != nil {
		return nil, true
	}

	lhServiceExport := &lighthousev2a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objMeta.GetName(),
			Namespace: objMeta.GetNamespace(),
		},
	}

	lhExportStatus := createServiceExportStatus(serviceExportCreated)
	if lhExportStatus == nil {
		return nil, false
	}

	lhServiceExport.Status = *lhExportStatus

	return lhServiceExport, false
}

func createServiceExportStatus(export *mcsv1a1.ServiceExport) *lighthousev2a1.ServiceExportStatus {
	if len(export.Status.Conditions) == 0 {
		return nil
	}
	var lhConditionsList []lighthousev2a1.ServiceExportCondition

	for _, status := range export.Status.Conditions {
		if status.Type == mcsv1a1.ServiceExportValid {
			lhStatus := lighthousev2a1.ServiceExportCondition{
				Type:               lighthousev2a1.ServiceExportExported,
				Status:             status.Status,
				LastTransitionTime: status.LastTransitionTime,
				Reason:             status.Reason,
				Message:            status.Message,
			}
			lhConditionsList = append(lhConditionsList, lhStatus)
		}
	}

	if len(lhConditionsList) != 0 {
		return &lighthousev2a1.ServiceExportStatus{
			Conditions: lhConditionsList,
		}
	}

	return nil
}
