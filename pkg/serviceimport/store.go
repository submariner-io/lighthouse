package serviceimport

import mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

type Store interface {
	Put(serviceImport *mcsv1a1.ServiceImport)

	Remove(serviceImport *mcsv1a1.ServiceImport)
}
