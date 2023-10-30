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
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/names"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	admversion "github.com/submariner-io/admiral/pkg/version"
	"github.com/submariner-io/lighthouse/pkg/agent/controller"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var (
	masterURL   string
	kubeConfig  string
	help        = false
	showVersion = false
	logger      = log.Logger{Logger: logf.Log.WithName("main")}
	version     = "not-compiled-properly"
)

func init() {
	flag.BoolVar(&help, "help", help, "Print usage options")
	flag.BoolVar(&showVersion, "version", showVersion, "Show version")
}

func exitOnError(err error, reason string) {
	if err == nil {
		return
	}

	logger.Error(err, "Failed to initialize.", "reason", reason)
	os.Exit(255)
}

func main() {
	agentSpec := controller.AgentSpecification{
		Verbosity: log.DEBUG,
	}
	err := envconfig.Process("submariner", &agentSpec)
	exitOnError(err, "Error processing env config for agent spec")

	if agentSpec.Debug {
		agentSpec.Verbosity = log.LIBDEBUG
	}

	// Set up verbosity based on environment variables
	os.Args = append(os.Args, fmt.Sprintf("-v=%d", agentSpec.Verbosity))

	kzerolog.AddFlags(nil)
	flag.Parse()

	if help {
		flag.PrintDefaults()
		return
	}

	admversion.Print(names.ServiceDiscoveryComponent, version)

	if showVersion {
		return
	}

	kzerolog.InitK8sLogging()

	// initialize klog as well, since some internal k8s packages still log with klog directly
	// we want at least the verbosity level to match what was requested
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	//nolint:errcheck // Ignore errors; CommandLine is set for ExitOnError.
	klogFlags.Parse(os.Args[1:])

	logger.Infof("Arguments: %v", os.Args)
	logger.Infof("AgentSpec: %#v", agentSpec)

	util.AddCertificateErrorHandler(agentSpec.HaltOnCertError)

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	exitOnError(err, "Error adding Multicluster v1alpha1 to the scheme")

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	exitOnError(err, "Error building kubeconfig")

	restMapper, err := util.BuildRestMapper(cfg)
	exitOnError(err, "Error building rest mapper")

	localClient, err := dynamic.NewForConfig(cfg)
	exitOnError(err, "Error creating dynamic client")

	// set up signals so we handle the first shutdown signal gracefully
	ctx := signals.SetupSignalHandler()

	lightHouseAgent, err := controller.New(&agentSpec, broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalClient:     localClient,
		RestMapper:      restMapper,
		Scheme:          scheme.Scheme,
	}, controller.AgentConfig{
		ServiceImportCounterName: "submariner_service_import",
		ServiceExportCounterName: "submariner_service_export",
	})
	exitOnError(err, "Failed to create lighthouse agent")

	if agentSpec.Uninstall {
		logger.Info("Uninstalling lighthouse")

		err := lightHouseAgent.Cleanup(ctx)
		exitOnError(err, "Error cleaning up the lighthouse agent controller")

		return
	}

	err = lightHouseAgent.Start(ctx.Done())
	exitOnError(err, "Failed to start lighthouse agent")

	httpServer := startHTTPServer()

	<-ctx.Done()

	logger.Info("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error(err, "Error shutting down metrics HTTP server")
	}
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func startHTTPServer() *http.Server {
	srv := &http.Server{Addr: ":8082", ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Error(err, "Error starting metrics server")
		}
	}()

	return srv
}
