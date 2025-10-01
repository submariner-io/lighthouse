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

package lighthouse

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/configmap"
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/admiral/pkg/names"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/lighthouse/coredns/gateway"
	"github.com/submariner-io/lighthouse/coredns/resolver"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	k8snet "k8s.io/utils/net"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var (
	masterURL  string
	kubeconfig string
)

var (
	buildKubeConfigFunc = clientcmd.BuildConfigFromFlags

	newK8sClient = func(cfg *rest.Config) (kubernetes.Interface, error) {
		return kubernetes.NewForConfig(cfg)
	}

	restMapper meta.RESTMapper
)

// init registers this plugin within the Caddy plugin framework. It uses "example" as the
// name, and couples it to the Action "setup".
func init() {
	utilruntime.Must(mcsv1a1.Install(scheme.Scheme))
	utilruntime.Must(discovery.AddToScheme(scheme.Scheme))

	caddy.RegisterPlugin(PluginName, caddy.Plugin{
		ServerType: "dns",
		Action:     setupLighthouse,
	})
}

// setup is the function that gets called when the config parser see the token "lighthouse". Setup is responsible
// for parsing any extra options the this plugin may have. The first token this function sees is "lighthouse".
func setupLighthouse(c *caddy.Controller) error {
	l, err := lighthouseParse(c)
	if err != nil {
		return plugin.Error(PluginName, err) //nolint:wrapcheck // No need to wrap this.
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		l.Next = next
		return l
	})

	return nil
}

func lighthouseParse(c *caddy.Controller) (*Lighthouse, error) {
	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		return nil, errors.Wrap(err, "error building kubeconfig")
	}

	gwController := gateway.NewController()

	localClient, err := resource.NewDynamicClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "error creating local client")
	}

	k8sClient, err := newK8sClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "error creating K8s client")
	}

	lh := &Lighthouse{
		TTL:                 defaultTTL,
		ClusterStatus:       gwController,
		Resolver:            resolver.New(gwController, localClient),
		SupportedIPFamilies: determineSupportedAddressTypes(),
	}

	resolverController := resolver.NewController(lh.Resolver)

	stopCh := make(chan struct{})
	ctx := wait.ContextForChannel(stopCh)

	c.OnShutdown(func() error {
		close(stopCh)
		gwController.Stop()
		resolverController.Stop()

		return nil
	})

	submNamespace := os.Getenv("SUBMARINER_NAMESPACE")

	configMap, err := configmap.Get(ctx, resource.ForConfigMap(k8sClient, submNamespace), names.LighthouseCoreDNSComponent)
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving ConfigMap")
	}

	global.Init(configMap)

	configmap.WatchAndSignalOnChange(ctx, k8sClient, submNamespace, syscall.SIGINT, names.ServiceDiscoveryComponent)

	err = gwController.Start(localClient)
	if err != nil {
		return nil, errors.Wrap(err, "error starting the Gateway controller")
	}

	err = resolverController.Start(&watcher.Config{
		RestConfig: cfg,
		Client:     localClient,
		RestMapper: restMapper,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error starting the resolver controller")
	}

	err = lh.configure(c)

	return lh, err
}

func determineSupportedAddressTypes() []k8snet.IPFamily {
	var ipFamilies []k8snet.IPFamily

	cidrEnvVar := os.Getenv("SUBMARINER_CLUSTERCIDR")

	logger.Infof("SUBMARINER_CLUSTERCIDR env: %q", cidrEnvVar)

	for _, cidr := range strings.Split(cidrEnvVar, ",") {
		s := strings.TrimSpace(cidr)
		if s != "" {
			ipFamilies = append(ipFamilies, k8snet.IPFamilyOfCIDRString(strings.TrimSpace(cidr)))
		}
	}

	if len(ipFamilies) == 0 {
		ipFamilies = []k8snet.IPFamily{k8snet.IPv4}
	}

	logger.Infof("Supported IP families: %v\n", ipFamilies)

	return ipFamilies
}

func parseTTL(c *caddy.Controller) (uint32, error) {
	// Refer: https://github.com/coredns/coredns/blob/master/plugin/kubernetes/setup.go
	args := c.RemainingArgs()
	if len(args) == 0 {
		return 0, c.ArgErr() //nolint:wrapcheck // No need to wrap this.
	}

	t, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil {
		return 0, errors.Wrap(err, "error parsing TTL")
	}

	if t < 0 || t > 3600 {
		return 0, c.Errf("ttl must be in range [0, 3600]: %d", t) //nolint:wrapcheck // No need to wrap this.
	}

	return uint32(t), nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
