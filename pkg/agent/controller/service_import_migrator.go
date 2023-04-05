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
	"fmt"

	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/lighthouse/pkg/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	LegacySourceNameLabel    = "lighthouse.submariner.io/sourceName"
	LegacySourceClusterLabel = "lighthouse.submariner.io/sourceCluster"
)

// ServiceImportMigrator handles migration from the legacy per-cluster ServiceImports to aggregated ServiceImports added in 0.15.
type ServiceImportMigrator struct {
	brokerClient                       dynamic.ResourceInterface
	listLocalServiceImports            func() ([]runtime.Object, error)
	clusterID                          string
	localNamespace                     string
	converter                          converter
	deletedLocalServiceImportsOnBroker sets.Set[string]
}

func (c *ServiceImportMigrator) onRemoteServiceImport(serviceImport *mcsv1a1.ServiceImport) (runtime.Object, bool) {
	// Ignore a legacy ServiceImport from the broker for this cluster.
	if serviceImport.Labels[LegacySourceClusterLabel] == c.clusterID {
		return nil, false
	}

	// Remote legacy ServiceImports are synced to the local submariner namespace.
	serviceImport.Namespace = c.localNamespace

	return serviceImport, false
}

func (c *ServiceImportMigrator) onSuccessfulSyncFromBroker(obj runtime.Object, op syncer.Operation) bool {
	if op == syncer.Delete {
		return false
	}

	aggregatedServiceImport := obj.(*mcsv1a1.ServiceImport)

	_, ok := aggregatedServiceImport.Labels[LegacySourceNameLabel]
	if ok {
		// This is not an aggregated ServiceImport.
		return false
	}

	localServiceImportName := fmt.Sprintf("%s-%s-%s", aggregatedServiceImport.Name, aggregatedServiceImport.Namespace, c.clusterID)
	if c.deletedLocalServiceImportsOnBroker.Has(localServiceImportName) {
		// We've already deleted our legacy local ServiceImport from the broker for this service.
		return false
	}

	siList, err := c.listLocalServiceImports()
	if err != nil {
		logger.Error(err, "error listing legacy ServiceImports")
		return true
	}

	totalRemoteClusters := 0
	totalRemoteClustersUpgraded := 0
	foundLocalServiceImport := false

	for _, obj := range siList {
		si := obj.(*mcsv1a1.ServiceImport)

		if serviceImportSourceName(si) != aggregatedServiceImport.Name ||
			si.Labels[constants.LabelSourceNamespace] != aggregatedServiceImport.Namespace {
			continue
		}

		clusterID := sourceClusterName(si)
		if clusterID == c.clusterID {
			foundLocalServiceImport = true
			continue
		}

		totalRemoteClusters++

		if slices.IndexOf(aggregatedServiceImport.Status.Clusters, clusterID, clusterStatusKey) >= 0 {
			totalRemoteClustersUpgraded++
		}
	}

	if foundLocalServiceImport && totalRemoteClustersUpgraded == totalRemoteClusters {
		logger.Infof("All remote clusters have been upgraded for service \"%s/%s\" - removing local ServiceImport %q from the broker",
			aggregatedServiceImport.Namespace, aggregatedServiceImport.Name, localServiceImportName)

		err := c.brokerClient.Delete(context.Background(), localServiceImportName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "error deleting legacy ServiceImport from the broker")
			return true
		}

		c.deletedLocalServiceImportsOnBroker.Insert(localServiceImportName)
	}

	return false
}

func (c *ServiceImportMigrator) onLocalServiceImportDeleted(serviceImport *mcsv1a1.ServiceImport) error {
	if serviceImport.Labels[LegacySourceClusterLabel] != c.clusterID {
		return nil
	}

	logger.Infof("Legacy local ServiceImport %q deleted - removing from the broker", serviceImport.Name)

	err := c.brokerClient.Delete(context.Background(), serviceImport.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		err = nil
	}

	return err
}

func serviceImportSourceName(serviceImport *mcsv1a1.ServiceImport) string {
	// This function also checks the legacy source name label for migration.
	name, ok := serviceImport.Labels[mcsv1a1.LabelServiceName]
	if ok {
		return name
	}

	return serviceImport.Labels[LegacySourceNameLabel]
}

func sourceClusterName(serviceImport *mcsv1a1.ServiceImport) string {
	// This function also checks the legacy source cluster label for migration.
	name, ok := serviceImport.Labels[constants.MCSLabelSourceCluster]
	if ok {
		return name
	}

	return serviceImport.Labels[LegacySourceClusterLabel]
}
