package serviceimport

import lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"

type Store interface {
	Put(serviceImport *lighthousev2a1.ServiceImport)

	Remove(serviceImport *lighthousev2a1.ServiceImport)
}
