module github.com/submariner-io/lighthouse

go 1.12

require (
	github.com/caddyserver/caddy v1.0.1
	github.com/coredns/coredns v1.5.2
	github.com/golang/mock v1.3.1
	github.com/miekg/dns v1.1.15
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.9.0
	github.com/pkg/errors v0.9.1
	github.com/submariner-io/admiral v0.0.0-20190829090417-2e381e854f60
	github.com/submariner-io/shipyard v0.0.0-20200324112155-1429f74326da
	k8s.io/api v0.0.0-20190313235455-40a48860b5ab
	k8s.io/apimachinery v0.0.0-20190629003722-e20a3a656cff
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v0.3.3
	sigs.k8s.io/controller-runtime v0.1.12
)

replace github.com/bronze1man/goStrongswanVici => github.com/mangelajo/goStrongswanVici v0.0.0-20190701121157-9a5ae4453bda

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20190521190702-177766529176

replace k8s.io/api => k8s.io/api v0.0.0-20190222213804-5cb15d344471
