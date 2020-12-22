/*
© 2020 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"flag"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"k8s.io/client-go/kubernetes/scheme"

	lighthousev2a1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v2alpha1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var (
	masterURL  string
	kubeConfig string
)

func main() {
	agentSpec := controller.AgentSpecification{}

	klog.InitFlags(nil)

	flag.Parse()

	err := envconfig.Process("submariner", &agentSpec)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Infof("AgentSpec: %v", agentSpec)

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Exitf("Error adding Multicluster v1alpha1 to the scheme: %v", err)
	}

	err = lighthousev2a1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Exitf("Error adding lighthouse V2alpha1 to the scheme: %v", err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	klog.Infof("Starting submariner-lighthouse-agent %v", agentSpec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()
	lightHouseAgent, err := controller.New(&agentSpec, cfg)
	if err != nil {
		klog.Fatalf("Failed to create lighthouse agent: %v", err)
	}

	if err := lightHouseAgent.Start(stopCh); err != nil {
		klog.Fatalf("Failed to start lighthouse agent: %v", err)
	}

	<-stopCh

	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
