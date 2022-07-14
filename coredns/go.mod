module github.com/submariner-io/lighthouse/coredns

go 1.16

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/caddy v1.1.1
	github.com/coredns/coredns v1.8.4
	github.com/miekg/dns v1.1.48
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.19.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.2
	github.com/submariner-io/admiral v0.13.0-rc2
	github.com/submariner-io/lighthouse v0.13.0-rc2
	golang.org/x/tools v0.1.7 // indirect
	k8s.io/api v0.21.11
	k8s.io/apimachinery v0.21.11
	k8s.io/client-go v0.21.11
	k8s.io/klog v1.0.0
	sigs.k8s.io/mcs-api v0.1.0
)

// Pin to a non-local version of lighthouse. This is not ideal, but there's a limitation in Cachito.
// This will hopefully be fixed, but need a workaround for now.
// https://issues.redhat.com/browse/CLOUDBLD-10559
// Local project
// replace github.com/submariner-io/lighthouse => ../
