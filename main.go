package main

import (
	"flag"
	"os"

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/federate/kubefed"
	"github.com/submariner-io/lighthouse/pkg/controller"
	"github.com/submariner-io/submariner/pkg/signals"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
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

	federator := buildKubeFedFederator(stopCh)
	lightHouseController := controller.New(federator)

	lightHouseController.Start()

	<-stopCh

	lightHouseController.Stop()
	klog.Info("All controller stopped or exited. Stopping main loop")
}

func buildKubeFedFederator(stopCh <-chan struct{}) federate.Federator {
	kubeConfig, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error attempting to load kubeconfig: %s", err.Error())
	}
	federator, err := kubefed.New(kubeConfig,

		stopCh)
	if err != nil {
		klog.Fatalf("Error creating kubefed federator: %s", err.Error())
	}
	return federator
}
