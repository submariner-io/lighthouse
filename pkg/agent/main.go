package main

import (
	"flag"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

var (
	masterURL  string
	kubeConfig string
)

const defaultResync = 0 * time.Minute

func main() {
	var agentSpec controller.AgentSpecification
	klog.InitFlags(nil)
	flag.Parse()

	err := envconfig.Process("submariner", &agentSpec)
	if err != nil {
		klog.Fatal(err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}
	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("error building k8s clientset: %s", err.Error())
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(clientSet, defaultResync)
	informerConfig := controller.InformerConfigStruct{
		KubeClientSet:   clientSet,
		ServiceInformer: informerFactory.Core().V1().Services(),
	}
	klog.Infof("Starting submariner-lighthouse-agent %v", agentSpec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()
	lightHouseAgent := controller.New(&agentSpec, informerConfig)

	informerFactory.Start(stopCh)

	if err := lightHouseAgent.Run(stopCh); err != nil {
		klog.Fatalf("Failed to start lighthouse agent: %v", err)
	}

	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
