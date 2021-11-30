package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceImportWeights is the Schema for the serviceimportweights API
type ServiceImportWeights struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ServiceImportWeightsSpec `json:"spec,omitempty"`
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ServiceImportWeightsSpec defines the desired state of ServiceImportWeights
type ServiceImportWeightsSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Weights map of [src_cluster -> [namespace -> [svc_name -> [target_cluster -> weight]]]]
	SourceClusterWeighsMap map[string]*ClusterWeightMap `json:"source_cluster_weight_map,omitempty"`
}

type ClusterWeightMap struct {
	// Weights map of [namespace -> [svc_name -> [target_cluster -> weight]]]
	NamespaceWeightMap map[string]*NamespaceWeightMap `json:"namespace_weight_map,omitempty"`
}

type NamespaceWeightMap struct {
	// Weights map of [svc_name -> [target_cluster -> weight]]
	ServiceWeightMap map[string]ServiceWeightMap `json:"service_weight_map,omitempty"`
}

type ServiceWeightMap struct {
	// Weights map of [target_cluster -> weight]
	TargetClusterWeightMap map[string]int64 `json:"target_cluster_weight_map,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceImportWeightsList contains a list of ServiceImportWeights
type ServiceImportWeightsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceImportWeights `json:"items"`
}
