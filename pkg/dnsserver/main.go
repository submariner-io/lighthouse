package main

import (
	"flag"
	"fmt"
	"log"
	"strconv"

	"k8s.io/klog"

	"github.com/submariner-io/lighthouse/pkg/dnsserver/dnscontroller"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/miekg/dns"
)

var (
	masterURL  string
	kubeconfig string
)

var buildKubeConfigFunc = clientcmd.BuildConfigFromFlags

func main() {
	klog.Errorf("Starting DNSController Main method")
	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		fmt.Errorf("error building kubeconfig: %v", err)
		return
	}
	mcsMap := new(dnscontroller.MultiClusterServiceMap)
	dnsController := dnscontroller.NewController(mcsMap)
	err = dnsController.Start(cfg)
	if err != nil {
		fmt.Errorf("error starting the controller: %v", err)
		return
	}
	srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
	srv.Handler = &dnscontroller.Lighthouse{MultiClusterServices: mcsMap}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Failed to set udp listener %s\n", err.Error())
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
