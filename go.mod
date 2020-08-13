module github.com/submariner-io/lighthouse

go 1.13

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/coredns v1.6.7
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/miekg/dns v1.1.31
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/submariner-io/admiral v0.6.0-pre1
	github.com/submariner-io/shipyard v0.5.0
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.3.0
)

// Pinned to kubernetes-1.17.0
replace (
	k8s.io/api => k8s.io/api v0.17.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.0
	k8s.io/client-go => k8s.io/client-go v0.17.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.17.0
)
