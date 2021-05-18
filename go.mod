module github.com/submariner-io/lighthouse

go 1.13

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/caddy v1.1.1
	github.com/coredns/coredns v1.8.3
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/miekg/dns v1.1.42
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.12.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.10.0
	github.com/submariner-io/admiral v0.9.0-rc0.0.20210506112321-7cecd38836bf
	github.com/submariner-io/shipyard v0.9.1-0.20210506112346-25ace6c97da9
	go.uber.org/zap v1.15.0 // indirect
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.4.0 // indirect
	k8s.io/utils v0.0.0-20210305010621-2afb4311ab10
	sigs.k8s.io/controller-runtime v0.6.5
	sigs.k8s.io/mcs-api v0.1.0
)

// Pinned to kubernetes-1.19.10
replace (
	k8s.io/api => k8s.io/api v0.19.10
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.10
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.10
	k8s.io/client-go => k8s.io/client-go v0.19.10
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.10
)

// Pinned for coredns
replace google.golang.org/grpc => google.golang.org/grpc v1.29.1
