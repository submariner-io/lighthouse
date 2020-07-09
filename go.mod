module github.com/submariner-io/lighthouse

go 1.13

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/coredns v1.5.2
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/miekg/dns v1.1.29
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/submariner-io/admiral v0.4.2-0.20200706185424-d52c64ccd7ba
	github.com/submariner-io/shipyard v0.4.1
	k8s.io/api v0.0.0-20190918195907-bd6ac527cfd2
	k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v0.4.0
	sigs.k8s.io/controller-runtime v0.3.0
)

// Pinned to kubernetes-1.14.1
replace (
	k8s.io/api => k8s.io/api v0.0.0-20190409021203-6e4e0e4f393b
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190409022649-727a075fdec8
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190404173353-6a84e37a896d
	k8s.io/client-go => k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20190409023720-1bc0c81fa51d
)

replace (
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.31.1
	// Pinned to v2.10.0 (kubernetes-1.14.1) so https://proxy.golang.org can
	// resolve it correctly.
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v1.8.2-0.20190525122359-d20e84d0fb64
)
