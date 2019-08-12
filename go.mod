module github.com/submariner-io/lighthouse

go 1.12

require (
	github.com/caddyserver/caddy v1.0.1
	github.com/coredns/coredns v1.5.2
	github.com/miekg/dns v1.1.15
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/pkg/errors v0.8.1
	github.com/submariner-io/admiral v0.0.0-20190717183806-7f0ad3dca605
	github.com/submariner-io/submariner v0.0.0-20190708095718-350482d85dd4
	k8s.io/api v0.0.0-20190313235455-40a48860b5ab
	k8s.io/apimachinery v0.0.0-20190629003722-e20a3a656cff
	k8s.io/client-go v0.0.0-20190521190702-177766529176
	k8s.io/klog v0.3.3
	sigs.k8s.io/controller-runtime v0.1.12
)

replace github.com/bronze1man/goStrongswanVici => github.com/mangelajo/goStrongswanVici v0.0.0-20190223031456-9a5ae4453bd
