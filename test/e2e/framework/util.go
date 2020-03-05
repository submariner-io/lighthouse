package framework

import (
	"strings"

	"github.com/pkg/errors"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func loadConfig(configPath, context string) (*restclient.Config, *clientcmdapi.Config, error) {

	errs := []string{}

	for _, config := range strings.Split(configPath, ":") {
		rest_config, client_config, err := loadSingleConfig(config, context)
		if err == nil {
			return rest_config, client_config, nil
		}
		errs = append(errs, err.Error())
	}

	return nil, nil, errors.Errorf("error loading any kubeConfig %s for context %s: [%v]",
		configPath, context, errs)

}

func loadSingleConfig(configPath, context string) (*restclient.Config, *clientcmdapi.Config, error) {

	c, err := clientcmd.LoadFromFile(configPath)

	if err != nil {
		return nil, nil, errors.Errorf("error loading kubeConfig %s: %v", configPath, err.Error())
	}
	if context != "" {
		c.CurrentContext = context
	}

	cfg, err := clientcmd.NewDefaultClientConfig(*c, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, nil, errors.Errorf("error creating default client config: %v", err.Error())
	}
	return cfg, c, nil
}
