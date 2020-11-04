package discovery

import (
	"github.com/submariner-io/shipyard/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
)

type ClusterParams struct {
	Name                    string
	Index                   framework.ClusterIndex
	LocalPodsToRunRequestOn *corev1.PodList
	Services                []ServiceParams
}

type ServiceParams struct {
	Name      string
	ClusterIP string
	Namespace string
}

type DigRequest struct {
	Cluster             ClusterParams
	Service             ServiceParams
	Domains             []string
	ClusterDomainPrefix string
}

type DigResult struct {
	ExpectedResolvedIPs []string
}

type DigTest struct {
	Request DigRequest
	Result  DigResult
}
