/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
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

	// Handle environment variables:
	// SUBMARINER_VERBOSITY determines the verbosity level (1 by default)
	// SUBMARINER_DEBUG, if set to true, sets the verbosity level to 3
	if debug := os.Getenv("SUBMARINER_DEBUG"); debug == "true" {
		os.Args = append(os.Args, "-v=3")
	} else if verbosity := os.Getenv("SUBMARINER_VERBOSITY"); verbosity != "" {
		os.Args = append(os.Args, fmt.Sprintf("-v=%s", verbosity))
	} else {
		os.Args = append(os.Args, "-v=2")
	}

	klog.InitFlags(nil)

	flag.Parse()

	err := envconfig.Process("submariner", &agentSpec)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Infof("Arguments: %v", os.Args)
	klog.Infof("AgentSpec: %v", agentSpec)

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Exitf("Error adding Multicluster v1alpha1 to the scheme: %v", err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	restMapper, err := util.BuildRestMapper(cfg)
	if err != nil {
		klog.Fatal(err.Error())
	}

	localClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("error creating dynamic client: %v", err)
	}

	klog.Infof("Starting submariner-lighthouse-agent %v", agentSpec)

	// set up signals so we handle the first shutdown signal gracefully
	ctx := signals.SetupSignalHandler()

	lightHouseAgent, err := controller.New(&agentSpec, broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalClient:     localClient,
		RestMapper:      restMapper,
		Scheme:          scheme.Scheme,
	}, kubeClientSet,
		controller.AgentConfig{
			ServiceImportCounterName: "submariner_service_import",
			ServiceExportCounterName: "submariner_service_export",
		})
	if err != nil {
		klog.Fatalf("Failed to create lighthouse agent: %v", err)
	}

	if agentSpec.Uninstall {
		klog.Info("Uninstalling lighthouse")

		err := lightHouseAgent.Cleanup()
		if err != nil {
			klog.Fatalf("Error cleaning up the lighthouse agent controller: %+v", err)
		}

		return
	}

	if err := lightHouseAgent.Start(ctx.Done()); err != nil {
		klog.Fatalf("Failed to start lighthouse agent: %v", err)
	}

	httpServer := startHTTPServer()

	<-ctx.Done()

	klog.Info("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		klog.Errorf("Error shutting down metrics HTTP server: %v", err)
	}
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func startHTTPServer() *http.Server {
	srv := &http.Server{Addr: ":8082"}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			klog.Errorf("Error starting metrics server: %v", err)
		}
	}()

	return srv
}
