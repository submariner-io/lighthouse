package v1

import (
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/sample-controller/pkg/apis/myresource"
)

var SchemeGroupVersion = schema.GroupVersion{
	Group:   myresource.GroupName,
	Version: "v1",
}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		SchemeGroupVersion,
		&MultiClusterService{},
		&MultiClusterServiceList{},
	)

	meta_v1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
