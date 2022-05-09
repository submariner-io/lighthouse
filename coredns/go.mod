module github.com/submariner-io/lighthouse/coredns

go 1.13

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/caddy v1.1.1
	github.com/coredns/coredns v1.8.4
	github.com/miekg/dns v1.1.48
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.19.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.1
	github.com/submariner-io/lighthouse v0.12.0-m3.0.20220502131803-864fecb47b2f
	k8s.io/api v0.21.11
	k8s.io/apimachinery v0.21.11
	k8s.io/client-go v0.21.11
	k8s.io/klog v1.0.0
	sigs.k8s.io/mcs-api v0.1.0
)

// Local project
replace github.com/submariner-io/lighthouse => ../
