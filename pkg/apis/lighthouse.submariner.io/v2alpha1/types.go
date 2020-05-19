package v2alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Refer: https://github.com/kubernetes/enhancements/pull/1646

//+genclient
//+k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceExport declares that the associated service should be exported to
// other clusters.
type ServiceExport struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Status ServiceExportStatus `json:"status,omitempty"`
}

// ServiceExportStatus contains the current status of an export.
type ServiceExportStatus struct {
	// +optional
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +listType=map
	// +listMapKey=type
	Conditions []ServiceExportCondition `json:"conditions,omitempty"`
}

// ServiceExportConditionType identifies a specific condition.
type ServiceExportConditionType string

const (
	// ServiceExportInitialized means the service export has been noticed
	// by the controller, has passed validation, has appropriate finalizers
	// set, and any required supercluster resources like the IP have been
	// reserved
	ServiceExportInitialized ServiceExportConditionType = "Initialized"
	// ServiceExportExported means that the service referenced by this
	// service export has been synced to all clusters in the supercluster
	ServiceExportExported ServiceExportConditionType = "Exported"
)

// ServiceExportCondition contains details for the current condition of this
// service export.
//
// Once [#1624](https://github.com/kubernetes/enhancements/pull/1624) is
// merged, this will be replaced by metav1.Condition.
type ServiceExportCondition struct {
	Type ServiceExportConditionType `json:"type"`
	// Status is one of {"True", "False", "Unknown"}
	Status corev1.ConditionStatus `json:"status"`
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
	// +optional
	Reason *string `json:"reason,omitempty"`
	// +optional
	Message *string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

//ServiceExportList is a list of serviceexport objects.
type ServiceExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ServiceExport `json:"items"`
}

//+genclient
//+k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:noStatus

// ServiceImport declares that the specified service is imported from other clusters.
type ServiceImport struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec ServiceImportSpec `json:"spec,omitempty"`
}

// ServiceImportSpec contains the current status of an imported service and the
// information necessary to consume it.
type ServiceImportSpec struct {
	// +patchStrategy=merge
	// +patchMergeKey=port
	// +listType=map
	// +listMapKey=port
	// +listMapKey=protocol
	Ports []corev1.ServicePort `json:"ports"`
	// +optional
	// +patchStrategy=merge
	// +patchMergeKey=cluster
	// +listType=map
	// +listMapKey=cluster
	Clusters []ClusterSpec `json:"clusters"`
	// +optional
	IP string `json:"ip,omitempty"`
}

// ClusterSpec contains service configuration mapped to a specific cluster
type ClusterSpec struct {
	Cluster string `json:"cluster"`
	// +optional
	// +listType=set
	TopologyKeys []string `json:"topologyKeys"`
	// +optional
	PublishNotReadyAddresses bool `json:"publishNotReadyAddresses"`
	// +optional
	SessionAffinity corev1.ServiceAffinity `json:"sessionAffinity"`
	// +optional
	SessionAffinityConfig *corev1.SessionAffinityConfig `json:"sessionAffinityConfig"`
	// +optional
	IPs []string `json:"ips,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

//ServiceImportList is a list of serviceimport objects.
type ServiceImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ServiceImport `json:"items"`
}
