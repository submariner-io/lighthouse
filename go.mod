module github.com/submariner-io/lighthouse

go 1.13

require (
	github.com/caddyserver/caddy v1.0.5
	github.com/coredns/caddy v1.1.0
	github.com/coredns/coredns v1.8.3
	github.com/go-logr/logr v0.3.0 // indirect
	github.com/googleapis/gnostic v0.4.0 // indirect
	github.com/imdario/mergo v0.3.10 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/miekg/dns v1.1.42
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.12.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.10.0
	github.com/submariner-io/admiral v0.9.0-rc0
	github.com/submariner-io/shipyard v0.9.0-rc0
	go.uber.org/zap v1.15.0 // indirect
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	google.golang.org/appengine v1.6.6 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200615113413-eeeca48fe776 // indirect
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.4.0 // indirect
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.6.5
	sigs.k8s.io/mcs-api v0.0.0-20200908023942-d26176718973
)

// Pinned to kubernetes-1.17.0
replace (
	k8s.io/api => k8s.io/api v0.17.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.0
	k8s.io/client-go => k8s.io/client-go v0.17.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.17.0
)
