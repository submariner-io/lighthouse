package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type MultiClusterService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MultiClusterServiceSpec `json:"spec"`
}

type MultiClusterServiceSpec struct {
	ServiceName string                    `json:"service_name"`
	Items       []MultiClusterServiceInfo `json:"items"`
}

type MultiClusterServiceInfo struct {
	ClusterID     string `json:"cluster_id"`
	ClusterDomain string `json:"cluster_domain"`
	ServiceIP     string `json:"service_ip"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type MultiClusterServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []MultiClusterService `json:"items"`
}
