module github.com/submariner-io/lighthouse

go 1.16

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/caddy v1.1.1
	github.com/coredns/coredns v1.9.1
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/miekg/dns v1.1.48
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.19.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.1
	github.com/submariner-io/admiral v0.12.0-m3.0.20220223161649-65232cddf5c0
	github.com/submariner-io/shipyard v0.12.0-m3.0.20220217165059-b87e9080b8d1
	github.com/uw-labs/lichen v0.1.5
	k8s.io/api v0.23.4
	k8s.io/apimachinery v0.23.4
	k8s.io/client-go v0.23.4
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20211116205334-6203023598ed
	sigs.k8s.io/controller-runtime v0.7.2
	sigs.k8s.io/mcs-api v0.1.0
)

// Pinned for coredns
replace google.golang.org/grpc => google.golang.org/grpc v1.44.0

// CVE-2020-26160
// This shouldn't be needed once we upgrade CoreDNS; but see
// https://github.com/submariner-io/lighthouse/issues/576
replace github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt v3.2.1+incompatible
