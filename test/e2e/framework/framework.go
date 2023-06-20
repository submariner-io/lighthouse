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

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsClientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
)

const (
	clustersetDomain    = "clusterset.local"
	anyCount            = -1
	statefulServiceName = "nginx-ss"
	statefulSetName     = "web"
	not                 = " not"
)

var CheckedDomains = []string{clustersetDomain}

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
type Framework struct {
	*framework.Framework
}

var (
	MCSClients        []*mcsClientset.Clientset
	EndpointClients   []dynamic.ResourceInterface
	SubmarinerClients []dynamic.ResourceInterface
)

func init() {
	framework.AddBeforeSuite(beforeSuite)
}

// NewFramework creates a test framework.
func NewFramework(baseName string) *Framework {
	f := &Framework{Framework: framework.NewBareFramework(baseName)}

	BeforeEach(f.BeforeEach)

	AfterEach(func() {
		namespace := f.Namespace
		f.AfterEach()

		f.AwaitEndpointSlices(framework.ClusterB, "", namespace, 0, 0)
		f.AwaitEndpointSlices(framework.ClusterA, "", namespace, 0, 0)
	})

	return f
}

func beforeSuite() {
	framework.By("Creating lighthouse clients")

	for _, restConfig := range framework.RestConfigs {
		MCSClients = append(MCSClients, createLighthouseClient(restConfig))
		EndpointClients = append(EndpointClients, createEndpointClientSet(restConfig))
		SubmarinerClients = append(SubmarinerClients, createSubmarinerClientSet(restConfig))
	}

	framework.DetectGlobalnet()
}

func createLighthouseClient(restConfig *rest.Config) *mcsClientset.Clientset {
	clientSet, err := mcsClientset.NewForConfig(restConfig)
	Expect(err).To(Not(HaveOccurred()))

	return clientSet
}

func createEndpointClientSet(restConfig *rest.Config) dynamic.ResourceInterface {
	clientSet, err := dynamic.NewForConfig(restConfig)
	Expect(err).To(Not(HaveOccurred()))

	gvr, _ := schema.ParseResourceArg("endpoints.v1.submariner.io")
	endpointsClient := clientSet.Resource(*gvr).Namespace("submariner-operator")
	_, err = endpointsClient.List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return endpointsClient
}

func createSubmarinerClientSet(restConfig *rest.Config) dynamic.ResourceInterface {
	clientSet, err := dynamic.NewForConfig(restConfig)
	Expect(err).To(Not(HaveOccurred()))

	gvr, _ := schema.ParseResourceArg("submariners.v1alpha1.submariner.io")
	submarinersClient := clientSet.Resource(*gvr).Namespace("submariner-operator")
	_, err = submarinersClient.List(context.TODO(), metav1.ListOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return submarinersClient
}

func (f *Framework) NewServiceExport(cluster framework.ClusterIndex, name, namespace string) *mcsv1a1.ServiceExport {
	nginxServiceExport := mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	se := MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Creating serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	serviceExport := framework.AwaitUntil("create serviceExport", func() (interface{}, error) {
		return se.Create(context.TODO(), &nginxServiceExport, metav1.CreateOptions{})
	}, framework.NoopCheckResult).(*mcsv1a1.ServiceExport)

	return serviceExport
}

func (f *Framework) AwaitServiceExportedStatusCondition(cluster framework.ClusterIndex, name, namespace string) {
	By(fmt.Sprintf("Retrieving ServiceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))

	se := MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace)

	framework.AwaitUntil("retrieve ServiceExport", func() (interface{}, error) {
		return se.Get(context.TODO(), name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		se := result.(*mcsv1a1.ServiceExport)

		for i := range se.Status.Conditions {
			if se.Status.Conditions[i].Type == constants.ServiceExportReady {
				if se.Status.Conditions[i].Status != v1.ConditionTrue {
					out, _ := json.MarshalIndent(se.Status.Conditions[i], "", "  ")
					return false, fmt.Sprintf("ServiceExport %s condition status is %s", constants.ServiceExportReady, out), nil
				}

				return true, "", nil
			}
		}

		out, _ := json.MarshalIndent(se.Status.Conditions, "", " ")

		return false, fmt.Sprintf("ServiceExport %s condition status not found. Actual: %s",
			constants.ServiceExportReady, out), nil
	})
}

func (f *Framework) DeleteServiceExport(cluster framework.ClusterIndex, name, namespace string) {
	By(fmt.Sprintf("Deleting serviceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	framework.AwaitUntil("delete service export", func() (interface{}, error) {
		return nil, MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace).Delete(
			context.TODO(), name, metav1.DeleteOptions{})
	}, framework.NoopCheckResult)
}

func (f *Framework) GetService(cluster framework.ClusterIndex, name, namespace string) (*v1.Service, error) {
	By(fmt.Sprintf("Retrieving service %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	return framework.KubeClients[cluster].CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

func (f *Framework) AwaitAggregatedServiceImport(targetCluster framework.ClusterIndex, svc *v1.Service, clusterCount int) {
	By(fmt.Sprintf("Retrieving ServiceImport for %q in ns %q on %q", svc.Name, svc.Namespace,
		framework.TestContext.ClusterIDs[targetCluster]))

	si := MCSClients[targetCluster].MulticlusterV1alpha1().ServiceImports(svc.Namespace)

	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		obj, err := si.Get(context.TODO(), svc.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil //nolint:nilnil // Intentional
		}

		return obj, err
	}, func(result interface{}) (bool, string, error) {
		if clusterCount == 0 {
			if result != nil {
				return false, "ServiceImport still exists", nil
			}

			return true, "", nil
		}

		if result == nil {
			return false, "ServiceImport not found", nil
		}

		si := result.(*mcsv1a1.ServiceImport)

		if len(si.Status.Clusters) != clusterCount {
			return false, fmt.Sprintf("Actual cluster count %d does not match expected %d",
				len(si.Status.Clusters), clusterCount), nil
		}

		expPorts := make([]mcsv1a1.ServicePort, len(svc.Spec.Ports))
		for i := range svc.Spec.Ports {
			expPorts[i] = mcsv1a1.ServicePort{
				Name:     svc.Spec.Ports[i].Name,
				Protocol: svc.Spec.Ports[i].Protocol,
				Port:     svc.Spec.Ports[i].Port,
			}
		}

		if svc.Spec.ClusterIP != v1.ClusterIPNone && !slices.Equivalent(expPorts, si.Spec.Ports, func(p mcsv1a1.ServicePort) string {
			return fmt.Sprintf("%s%s%d", p.Name, p.Protocol, p.Port)
		}) {
			s1, _ := json.MarshalIndent(expPorts, "", "  ")
			s2, _ := json.MarshalIndent(si.Spec.Ports, "", "  ")

			return false, fmt.Sprintf("ServiceImport ports do not match. Expected: %s, Actual: %s", s1, s2), nil
		}

		return true, "", nil
	})
}

func (f *Framework) NewHeadlessServiceWithParams(name, portName string, protcol v1.Protocol,
	labelsMap map[string]string, cluster framework.ClusterIndex,
) *v1.Service {
	var port int32 = 80

	nginxService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.ServiceSpec{
			Type:      "ClusterIP",
			ClusterIP: v1.ClusterIPNone,
			Ports: []v1.ServicePort{
				{
					Port:     port,
					Name:     portName,
					Protocol: protcol,
				},
			},
		},
	}

	if len(labelsMap) != 0 {
		nginxService.Labels = labelsMap
		nginxService.Spec.Selector = labelsMap
	}

	sc := framework.KubeClients[cluster].CoreV1().Services(f.Namespace)
	service := framework.AwaitUntil("create service", func() (interface{}, error) {
		return sc.Create(context.TODO(), &nginxService, metav1.CreateOptions{})
	}, framework.NoopCheckResult).(*v1.Service)

	return service
}

func (f *Framework) NewNginxHeadlessService(cluster framework.ClusterIndex) *v1.Service {
	return f.NewHeadlessServiceWithParams("nginx-headless", "http", v1.ProtocolTCP,
		map[string]string{"app": "nginx-demo"}, cluster)
}

func (f *Framework) NewHeadlessServiceEndpointIP(cluster framework.ClusterIndex) *v1.Service {
	return f.NewHeadlessServiceWithParams("ep-headless", "http", v1.ProtocolTCP,
		map[string]string{}, cluster)
}

func (f *Framework) NewEndpointForHeadlessService(cluster framework.ClusterIndex, svc *v1.Service) {
	ep := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: svc.Name,
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "192.168.5.1",
						Hostname: "host1",
					},
					{
						IP:       "192.168.5.2",
						Hostname: "host2",
					},
				},
			},
		},
	}

	for i := range svc.Spec.Ports {
		ep.Subsets[0].Ports = append(ep.Subsets[0].Ports, v1.EndpointPort{
			Name:     svc.Spec.Ports[i].Name,
			Port:     svc.Spec.Ports[i].Port,
			Protocol: svc.Spec.Ports[i].Protocol,
		})
	}

	ec := framework.KubeClients[cluster].CoreV1().Endpoints(svc.Namespace)
	framework.AwaitUntil("create endpoint", func() (interface{}, error) {
		return ec.Create(context.TODO(), ep, metav1.CreateOptions{})
	}, framework.NoopCheckResult)
}

func (f *Framework) AwaitEndpointIPs(targetCluster framework.ClusterIndex, name,
	namespace string, count int,
) (ipList, hostNameList []string) {
	ep := framework.KubeClients[targetCluster].CoreV1().Endpoints(namespace)
	By(fmt.Sprintf("Retrieving Endpoints for %s on %q", name, framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve Endpoints", func() (interface{}, error) {
		return ep.Get(context.TODO(), name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		ipList = make([]string, 0)
		hostNameList = make([]string, 0)
		endpoint := result.(*v1.Endpoints)
		for _, eps := range endpoint.Subsets {
			for _, addr := range eps.Addresses {
				ipList = append(ipList, addr.IP)
				switch {
				case addr.Hostname != "":
					hostNameList = append(hostNameList, addr.Hostname)
				case addr.TargetRef != nil:
					hostNameList = append(hostNameList, addr.TargetRef.Name)
				}
			}
		}
		if count != anyCount && len(ipList) != count {
			return false, fmt.Sprintf("endpoints have %d IPs when expected %d", len(ipList), count), nil
		}
		return true, "", nil
	})

	return ipList, hostNameList
}

func (f *Framework) AwaitPodIngressIPs(targetCluster framework.ClusterIndex, svc *v1.Service, count int,
	isLocal bool,
) (ipList, hostNameList []string) {
	podList := f.Framework.AwaitPodsByAppLabel(targetCluster, svc.Labels["app"], svc.Namespace, count)
	hostNameList = make([]string, 0)
	ipList = make([]string, 0)

	for i := 0; i < len(podList.Items); i++ {
		ingressIPName := fmt.Sprintf("pod-%s", podList.Items[i].Name)
		ingressIP := f.Framework.AwaitGlobalIngressIP(targetCluster, ingressIPName, svc.Namespace)

		if isLocal {
			ipList = append(ipList, podList.Items[i].Status.PodIP)
		} else {
			ipList = append(ipList, ingressIP)
		}

		hostname := podList.Items[i].Spec.Hostname
		if hostname == "" {
			hostname = podList.Items[i].Name
		}

		hostNameList = append(hostNameList, hostname)
	}

	return ipList, hostNameList
}

func (f *Framework) AwaitPodIPs(targetCluster framework.ClusterIndex, svc *v1.Service, count int,
	isLocal bool,
) (ipList, hostNameList []string) {
	if framework.TestContext.GlobalnetEnabled {
		return f.AwaitPodIngressIPs(targetCluster, svc, count, isLocal)
	}

	return f.AwaitEndpointIPs(targetCluster, svc.Name, svc.Namespace, count)
}

func (f *Framework) GetPodIPs(targetCluster framework.ClusterIndex, service *v1.Service, isLocal bool) (ipList, hostNameList []string) {
	return f.AwaitPodIPs(targetCluster, service, anyCount, isLocal)
}

func (f *Framework) AwaitEndpointIngressIPs(targetCluster framework.ClusterIndex, svc *v1.Service) (ipList, hostNameList []string) {
	hostNameList = make([]string, 0)
	ipList = make([]string, 0)

	endpoint := framework.AwaitUntil("retrieve Endpoints", func() (interface{}, error) {
		return framework.KubeClients[targetCluster].CoreV1().Endpoints(svc.Namespace).Get(context.TODO(), svc.Name, metav1.GetOptions{})
	}, framework.NoopCheckResult).(*v1.Endpoints)

	for _, subset := range endpoint.Subsets {
		for _, address := range subset.Addresses {
			// Add hostname to hostNameList
			if address.Hostname != "" {
				hostNameList = append(hostNameList, address.Hostname)
			}

			// Add globalIP to ipList
			gip := f.Framework.AwaitGlobalIngressIP(targetCluster, fmt.Sprintf("ep-%.44s-%.15s", endpoint.Name, address.IP), svc.Namespace)
			ipList = append(ipList, gip)
		}
	}

	return ipList, hostNameList
}

func (f *Framework) GetEndpointIPs(targetCluster framework.ClusterIndex, svc *v1.Service) (ipList, hostNameList []string) {
	if framework.TestContext.GlobalnetEnabled {
		return f.AwaitEndpointIngressIPs(targetCluster, svc)
	}

	return f.AwaitEndpointIPs(targetCluster, svc.Name, svc.Namespace, anyCount)
}

func (f *Framework) SetNginxReplicaSet(cluster framework.ClusterIndex, count uint32) *appsv1.Deployment {
	By(fmt.Sprintf("Setting Nginx deployment replicas to %v in cluster %q", count, framework.TestContext.ClusterIDs[cluster]))
	patch := fmt.Sprintf(`{"spec":{"replicas":%v}}`, count)
	deployments := framework.KubeClients[cluster].AppsV1().Deployments(f.Namespace)
	result := framework.AwaitUntil("set replicas", func() (interface{}, error) {
		return deployments.Patch(context.TODO(), "nginx-demo", types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	}, framework.NoopCheckResult).(*appsv1.Deployment)

	return result
}

func (f *Framework) NewNginxStatefulSet(cluster framework.ClusterIndex) *appsv1.StatefulSet {
	var replicaCount int32 = 1
	var port int32 = 8080

	nginxStatefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: statefulSetName,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": statefulServiceName,
				},
			},
			ServiceName: statefulServiceName,
			Replicas:    &replicaCount,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": statefulServiceName,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            statefulServiceName,
							Image:           framework.TestContext.NettestImageURL,
							ImagePullPolicy: v1.PullAlways,
							Ports: []v1.ContainerPort{
								{
									ContainerPort: port,
									Name:          "web",
								},
							},
							Command: []string{"/app/simpleserver"},
						},
					},
					RestartPolicy: v1.RestartPolicyAlways,
				},
			},
		},
	}

	create(f, cluster, nginxStatefulSet)

	return nginxStatefulSet
}

func create(f *Framework, cluster framework.ClusterIndex, statefulSet *appsv1.StatefulSet) *v1.PodList {
	count := statefulSet.Spec.Replicas
	pc := framework.KubeClients[cluster].AppsV1().StatefulSets(f.Namespace)
	appName := statefulSet.Spec.Template.ObjectMeta.Labels["app"]

	_ = framework.AwaitUntil("create statefulSet", func() (interface{}, error) {
		return pc.Create(context.TODO(), statefulSet, metav1.CreateOptions{})
	}, framework.NoopCheckResult).(*appsv1.StatefulSet)

	return f.AwaitPodsByAppLabel(cluster, appName, f.Namespace, int(*count))
}

func (f *Framework) AwaitEndpointSlices(targetCluster framework.ClusterIndex, name, namespace string,
	expSliceCount, expReadyCount int,
) (endpointSliceList *discovery.EndpointSliceList) {
	ep := framework.KubeClients[targetCluster].DiscoveryV1().EndpointSlices(namespace)
	labelMap := map[string]string{
		discovery.LabelManagedBy: constants.LabelValueManagedBy,
	}
	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelMap).String(),
	}

	By(fmt.Sprintf("Retrieving EndpointSlices for %q in ns %q on %q", name, namespace,
		framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve EndpointSlices", func() (interface{}, error) {
		return ep.List(context.TODO(), listOptions)
	}, func(result interface{}) (bool, string, error) {
		endpointSliceList = result.(*discovery.EndpointSliceList)
		sliceCount := 0
		readyCount := 0

		for i := range endpointSliceList.Items {
			es := &endpointSliceList.Items[i]
			if name == "" || es.Labels[mcsv1a1.LabelServiceName] == name {
				sliceCount++

				for j := range es.Endpoints {
					if es.Endpoints[j].Conditions.Ready == nil || *es.Endpoints[j].Conditions.Ready {
						readyCount++
					}
				}
			}
		}

		if expSliceCount != anyCount && sliceCount != expSliceCount {
			return false, fmt.Sprintf("%d EndpointSlices found when expected %d", sliceCount, expSliceCount), nil
		}

		if expReadyCount != anyCount && readyCount != expReadyCount {
			return false, fmt.Sprintf("%d ready Endpoints found when expected %d", readyCount, expReadyCount), nil
		}

		return true, "", nil
	})

	return endpointSliceList
}

func (f *Framework) SetNginxStatefulSetReplicas(cluster framework.ClusterIndex, count uint32) *appsv1.StatefulSet {
	By(fmt.Sprintf("Setting Nginx statefulset replicas to %v", count))
	patch := fmt.Sprintf(`{"spec":{"replicas":%v}}`, count)
	ss := framework.KubeClients[cluster].AppsV1().StatefulSets(f.Namespace)
	result := framework.AwaitUntil("set replicas", func() (interface{}, error) {
		return ss.Patch(context.TODO(), statefulSetName, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	}, framework.NoopCheckResult).(*appsv1.StatefulSet)

	return result
}

func (f *Framework) GetHealthCheckIPInfo(cluster framework.ClusterIndex) (endpointName, healthCheckIP string) {
	framework.AwaitUntil("Get healthCheckIP", func() (interface{}, error) {
		unstructuredEndpointList, err := EndpointClients[cluster].List(context.TODO(), metav1.ListOptions{})
		return unstructuredEndpointList, err
	}, func(result interface{}) (bool, string, error) {
		unstructuredEndpointList := result.(*unstructured.UnstructuredList)
		for _, endpoint := range unstructuredEndpointList.Items {
			By(fmt.Sprintf("Getting the endpoint %s, for cluster %s", endpoint.GetName(), framework.TestContext.ClusterIDs[cluster]))

			if strings.Contains(endpoint.GetName(), framework.TestContext.ClusterIDs[cluster]) {
				endpointName = endpoint.GetName()

				var found bool
				var err error
				healthCheckIP, found, err = unstructured.NestedString(endpoint.Object, "spec", "healthCheckIP")

				if err != nil {
					return false, "", err
				}

				if !found {
					return false, fmt.Sprintf("HealthcheckIP not found in %#v ", endpoint), nil
				}
			}
		}
		return true, "", nil
	})

	return endpointName, healthCheckIP
}

func (f *Framework) GetHealthCheckEnabledInfo(cluster framework.ClusterIndex) (healthCheckEnabled bool) {
	framework.AwaitUntil("Get healthCheckEnabled Configuration", func() (interface{}, error) {
		unstructuredSubmarinerConfig, err := SubmarinerClients[cluster].Get(context.TODO(),
			"submariner", metav1.GetOptions{})
		return unstructuredSubmarinerConfig, err
	}, func(result interface{}) (bool, string, error) {
		unstructuredSubmarinerConfig := result.(*unstructured.Unstructured)
		By(fmt.Sprintf("Getting the Submariner Config, for cluster %s", framework.TestContext.ClusterIDs[cluster]))
		var found bool
		var err error
		healthCheckEnabled, found, err = unstructured.NestedBool(unstructuredSubmarinerConfig.Object,
			"spec", "connectionHealthCheck", "enabled")

		if err != nil {
			return false, "", err
		}

		if !found {
			return true, "", nil
		}
		return true, "", nil
	})

	return healthCheckEnabled
}

func (f *Framework) SetHealthCheckIP(cluster framework.ClusterIndex, ip, endpointName string) {
	By(fmt.Sprintf("Setting health check IP cluster %q to %v", framework.TestContext.ClusterIDs[cluster], ip))
	patch := fmt.Sprintf(`{"spec":{"healthCheckIP":%q}}`, ip)

	framework.AwaitUntil("set healthCheckIP", func() (interface{}, error) {
		endpoint, err := EndpointClients[cluster].Patch(context.TODO(), endpointName, types.MergePatchType, []byte(patch),
			metav1.PatchOptions{})
		return endpoint, err
	}, framework.NoopCheckResult)
}

func (f *Framework) VerifyServiceIPWithDig(srcCluster, targetCluster framework.ClusterIndex, service *v1.Service, targetPod *v1.PodList,
	domains []string, clusterName string, shouldContain bool,
) {
	serviceIP := f.GetServiceIP(targetCluster, service)
	f.VerifyIPWithDig(srcCluster, service, targetPod, domains, clusterName, serviceIP, shouldContain)
}

func (f *Framework) VerifyIPWithDig(srcCluster framework.ClusterIndex, service *v1.Service, targetPod *v1.PodList,
	domains []string, clusterName, serviceIP string, shouldContain bool,
) {
	cmd := []string{"dig", "+short"}

	var clusterDNSName string
	if clusterName != "" {
		clusterDNSName = clusterName + "."
	}

	for i := range domains {
		cmd = append(cmd, clusterDNSName+service.Name+"."+f.Namespace+".svc."+domains[i])
	}

	op := "is"
	if !shouldContain {
		op += not
	}

	By(fmt.Sprintf("Executing %q to verify IP %q for service %q %q discoverable", strings.Join(cmd, " "), serviceIP, service.Name, op))
	framework.AwaitUntil("verify if service IP is discoverable", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(context.TODO(), &framework.ExecOptions{
			Command:       cmd,
			Namespace:     targetPod.Items[0].Namespace,
			PodName:       targetPod.Items[0].Name,
			ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
			CaptureStdout: true,
			CaptureStderr: true,
		}, srcCluster)
		if err != nil {
			return nil, err
		}

		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		doesContain := strings.Contains(result.(string), serviceIP)
		By(fmt.Sprintf("Validating that dig result %q %s %q", result, op, serviceIP))
		if doesContain && !shouldContain {
			return false, fmt.Sprintf("expected execution result %q not to contain %q", result, serviceIP), nil
		}

		if !doesContain && shouldContain {
			return false, fmt.Sprintf("expected execution result %q to contain %q", result, serviceIP), nil
		}

		return true, "", nil
	})
}

func (f *Framework) VerifyIPsWithDig(cluster framework.ClusterIndex, service *v1.Service, targetPod *v1.PodList,
	ipList, domains []string, clusterName string, shouldContain bool,
) {
	cmd := []string{"dig", "+short"}

	var clusterDNSName string
	if clusterName != "" {
		clusterDNSName = clusterName + "."
	}

	for i := range domains {
		cmd = append(cmd, clusterDNSName+service.Name+"."+f.Namespace+".svc."+domains[i])
	}

	op := "are"
	if !shouldContain {
		op += not
	}

	By(fmt.Sprintf("Executing %q to verify IPs %v for service %q %q discoverable", strings.Join(cmd, " "), ipList, service.Name, op))
	framework.AwaitUntil(" service IP verification", func() (interface{}, error) {
		stdout, _, err := f.ExecWithOptions(context.TODO(), &framework.ExecOptions{
			Command:       cmd,
			Namespace:     f.Namespace,
			PodName:       targetPod.Items[0].Name,
			ContainerName: targetPod.Items[0].Spec.Containers[0].Name,
			CaptureStdout: true,
			CaptureStderr: true,
		}, cluster)
		if err != nil {
			return nil, err
		}

		return stdout, nil
	}, func(result interface{}) (bool, string, error) {
		By(fmt.Sprintf("Validating that dig result %s %q", op, result))
		if len(ipList) == 0 && result != "" {
			return false, fmt.Sprintf("expected execution result %q to be empty", result), nil
		}
		for _, ip := range ipList {
			doesContain := strings.Contains(result.(string), ip)
			if doesContain && !shouldContain {
				return false, fmt.Sprintf("expected execution result %q not to contain %q", result, ip), nil
			}

			if !doesContain && shouldContain {
				return false, fmt.Sprintf("expected execution result %q to contain %q", result, ip), nil
			}
		}

		return true, "", nil
	})
}

func (f *Framework) GetServiceIP(svcCluster framework.ClusterIndex, service *v1.Service) string {
	Expect(service.Spec.Type).To(Equal(v1.ServiceTypeClusterIP))

	if !framework.TestContext.GlobalnetEnabled {
		return service.Spec.ClusterIP
	}

	return f.Framework.AwaitGlobalIngressIP(svcCluster, service.Name, service.Namespace)
}
