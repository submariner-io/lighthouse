package main

import (
	"flag"
	"os"

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/submariner/pkg/signals"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/federate/kubefed"
)

var kubeConfig string
var masterURL string

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", os.Getenv("KUBECONFIG"),
		"Path to kubeconfig containing embedded authinfo.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.V(2).Info("Starting lighthouse")

	stopCh := signals.SetupSignalHandler()
	runStoppedCh := make(chan struct{})

	//TODO need to be changed while implementing.
	_ = buildKubeFedFederator(stopCh)
	klog.Info("Successfully created federator")
	<-runStoppedCh
	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func buildKubeFedFederator(stopCh <-chan struct{}) federate.Federator {
	kubeConfig, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error attempting to load kubeconfig: %s", err.Error())
	}
	federator, err := kubefed.New(kubeConfig)
	if err != nil {
		klog.Fatalf("Error creating kubefed federator: %s", err.Error())
	}
	return federator
}
