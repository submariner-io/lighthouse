package discovery

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	submarinerIpamGlobalIP = "submariner.io/globalIp"
)

func digCommand(request DigRequest) []string {
	return constructDNSDigCommand(request.Service.Name, request.Service.Namespace, request.ClusterDomainPrefix, request.Domains)
}

func constructDNSDigCommand(serviceName, namespace, optionalClusterDomainPrefix string, domains []string) []string {
	dnsNames := constructDNSNames(serviceName, namespace, optionalClusterDomainPrefix, domains)
	cmd := []string{"dig", "+short"}

	for i := range dnsNames {
		cmd = append(cmd, dnsNames[i])
	}

	return cmd
}

func constructDNSNames(serviceName, namespace, optionalClusterDomainPrefix string, domains []string) []string {
	dnsNames := []string{}
	baseDNSName := serviceName + "." + namespace + ".svc."

	for i := range domains {
		dnsName := baseDNSName + domains[i]
		if optionalClusterDomainPrefix != "" {
			dnsName = optionalClusterDomainPrefix + "." + dnsName
		}

		dnsNames = append(dnsNames, dnsName)
	}

	return dnsNames
}

func extractServiceClusterIP(service *corev1.Service) string {
	var serviceIP string
	var ok bool

	if serviceIP, ok = service.Annotations[submarinerIpamGlobalIP]; !ok {
		serviceIP = service.Spec.ClusterIP
	}

	return serviceIP
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}

	return false
}
