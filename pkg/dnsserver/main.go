package main

import (
	"flag"
	"strconv"

	"github.com/miekg/dns"
	"github.com/submariner-io/lighthouse/pkg/dnsserver/handler"
	"github.com/submariner-io/lighthouse/pkg/multiclusterservice"
	"github.com/submariner-io/submariner/pkg/signals"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var (
	masterURL  string
	kubeconfig string
)

var buildKubeConfigFunc = clientcmd.BuildConfigFromFlags

func main() {
	klog.Infof("Starting DNSController")
	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("error building kubeconfig: %v", err)
		return
	}
	stopCh := signals.SetupSignalHandler()

	mcsMap := new(multiclusterservice.Map)
	mcsController := multiclusterservice.NewController(mcsMap)
	err = mcsController.Start(cfg)
	if err != nil {
		klog.Fatalf("Error starting the controller: %v", err)
		return
	}

	srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
	srv.Handler = handler.New(mcsMap)
	if err := srv.ListenAndServe(); err != nil {
		klog.Fatalf("Failed to set udp listener %s\n", err.Error())
		return
	}

	<-stopCh
	mcsController.Stop()
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
