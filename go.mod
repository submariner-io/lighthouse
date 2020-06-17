module github.com/submariner-io/lighthouse

go 1.12

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/coredns v1.5.2
	github.com/elazarl/goproxy v0.0.0-20200426045556-49ad98f6dac1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/miekg/dns v1.1.29
	github.com/onsi/ginkgo v1.13.0
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/submariner-io/admiral v0.3.1-0.20200617155518-f51143431ba7
	github.com/submariner-io/shipyard v0.3.0
	k8s.io/api v0.18.2
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.6.0
)

replace github.com/bronze1man/goStrongswanVici => github.com/mangelajo/goStrongswanVici v0.0.0-20190701121157-9a5ae4453bda

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20190521190702-177766529176

replace k8s.io/api => k8s.io/api v0.0.0-20190222213804-5cb15d344471
