package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//+genclient
//+k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MultiClusterService describes the information of service that is available across
// clusters. A shared informer running in the lighthouse control plane will listen for
// service creation in every cluster that is part of the control plane and whenever a
// service is created a MultiClusterService will be created. Lighthouse will
// distribute the MultiClusterService to every other cluster that is the part of the
// control plane. When the service is updated/deleted lighthouse ensures that the
// updated state is reflected in every cluster.
type MultiClusterService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MultiClusterServiceSpec `json:"spec"`
}

// MultiClusterServiceSpec provides the specification of a MultiClusterService
type MultiClusterServiceSpec struct {
	// List of service information across clusters. This field is not optional.
	Items []ClusterServiceInfo `json:"clusterServiceInfo"`
}

// ClusterServiceInfo provides information about service in each cluster.
type ClusterServiceInfo struct {
	// The unique identity of the cluster where the service is available. This field is
	// not optional.
	ClusterID string `json:"clusterID"`

	// The DNS zone in which the service is a part of. This field is not optional.
	ClusterDomain string `json:"clusterDomain"`

	// The cluster IP of the service running in the cluster that is identified by the
	// ClusterID field. This field is not optional.
	ServiceIP string `json:"serviceIP"`

	//+optional
	// The port in which the service listens at.This is an optional field.
	Port int `json:"port,omitempty"`
}

//+k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MultiClusterServiceList is a list of multiclusterservice objects.
type MultiClusterServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []MultiClusterService `json:"items"`
}
