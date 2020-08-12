package main

// build with external golang source code
// https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/#build-with-external-golang-source-code

import (
	_ "github.com/caddyserver/caddy/onevent"
	_ "github.com/coredns/coredns/plugin/any"
	_ "github.com/coredns/coredns/plugin/auto"
	_ "github.com/coredns/coredns/plugin/autopath"
	_ "github.com/coredns/coredns/plugin/bind"
	_ "github.com/coredns/coredns/plugin/cache"
	_ "github.com/coredns/coredns/plugin/cancel"
	_ "github.com/coredns/coredns/plugin/chaos"
	_ "github.com/coredns/coredns/plugin/debug"
	_ "github.com/coredns/coredns/plugin/dnssec"
	_ "github.com/coredns/coredns/plugin/dnstap"
	_ "github.com/coredns/coredns/plugin/erratic"
	_ "github.com/coredns/coredns/plugin/errors"
	_ "github.com/coredns/coredns/plugin/etcd"
	_ "github.com/coredns/coredns/plugin/file"
	_ "github.com/coredns/coredns/plugin/forward"
	_ "github.com/coredns/coredns/plugin/grpc"
	_ "github.com/coredns/coredns/plugin/health"
	_ "github.com/coredns/coredns/plugin/hosts"
	_ "github.com/coredns/coredns/plugin/k8s_external"
	_ "github.com/coredns/coredns/plugin/loadbalance"
	_ "github.com/coredns/coredns/plugin/log"
	_ "github.com/coredns/coredns/plugin/loop"
	_ "github.com/coredns/coredns/plugin/metadata"
	_ "github.com/coredns/coredns/plugin/metrics"
	_ "github.com/coredns/coredns/plugin/nsid"
	_ "github.com/coredns/coredns/plugin/pprof"
	_ "github.com/coredns/coredns/plugin/ready"
	_ "github.com/coredns/coredns/plugin/reload"
	_ "github.com/coredns/coredns/plugin/rewrite"
	_ "github.com/coredns/coredns/plugin/root"
	_ "github.com/coredns/coredns/plugin/route53"
	_ "github.com/coredns/coredns/plugin/secondary"
	_ "github.com/coredns/coredns/plugin/template"
	_ "github.com/coredns/coredns/plugin/tls"
	_ "github.com/coredns/coredns/plugin/trace"
	_ "github.com/coredns/coredns/plugin/whoami"
	_ "github.com/submariner-io/lighthouse/plugin/lighthouse"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

var directives = []string{
	"metadata",
	"cancel",
	"tls",
	"reload",
	"nsid",
	"root",
	"bind",
	"debug",
	"trace",
	"ready",
	"health",
	"pprof",
	"metrics",
	"errors",
	"log",
	"dnstap",
	"any",
	"chaos",
	"loadbalance",
	"cache",
	"rewrite",
	"dnssec",
	"autopath",
	"template",
	"hosts",
	"route53",
	"k8s_external",
	"lighthouse",
	"file",
	"auto",
	"secondary",
	"etcd",
	"loop",
	"forward",
	"grpc",
	"erratic",
	"whoami",
	"on",
}

func init() {
	dnsserver.Directives = directives
}

func main() {
	coremain.Run()
}
