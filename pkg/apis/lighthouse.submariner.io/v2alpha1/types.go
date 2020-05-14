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
