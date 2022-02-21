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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/discovery/v1beta1"
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
	labelSourceName      = "lighthouse.submariner.io/sourceName"
	labelSourceNamespace = "lighthouse.submariner.io/sourceNamespace"
	anyCount             = -1
	statefulServiceName  = "nginx-ss"
	statefulSetName      = "web"
	not                  = " not"
)

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
	se := MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Retrieving ServiceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	framework.AwaitUntil("retrieve ServiceExport", func() (interface{}, error) {
		return se.Get(context.TODO(), name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		se := result.(*mcsv1a1.ServiceExport)
		if len(se.Status.Conditions) == 0 {
			return false, "No ServiceExportConditions", nil
		}

		last := se.Status.Conditions[len(se.Status.Conditions)-1]
		if last.Type != mcsv1a1.ServiceExportValid {
			return false, fmt.Sprintf("ServiceExportCondition Type is %v", last.Type), nil
		}

		if last.Status != v1.ConditionTrue {
			return false, fmt.Sprintf("ServiceExportCondition Status is %v", last.Status), nil
		}

		return true, "", nil
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

func (f *Framework) AwaitServiceImportIP(srcCluster, targetCluster framework.ClusterIndex, svc *v1.Service) *mcsv1a1.ServiceImport {
	serviceIP := f.GetServiceIP(srcCluster, svc, false)

	return f.AwaitServiceImportWithIP(targetCluster, svc, serviceIP)
}

func (f *Framework) AwaitServiceImportWithIP(targetCluster framework.ClusterIndex, svc *v1.Service,
	serviceIP string) *mcsv1a1.ServiceImport {
	var retServiceImport *mcsv1a1.ServiceImport

	siNamePrefix := svc.Name + "-" + svc.Namespace + "-"
	si := MCSClients[targetCluster].MulticlusterV1alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	By(fmt.Sprintf("Retrieving ServiceImport for %s on %q", siNamePrefix, framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		return si.List(context.TODO(), metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		if len(siList.Items) < 1 {
			return false, fmt.Sprintf("ServiceImport with name prefix %s not found", siNamePrefix), nil
		}
		for i := range siList.Items {
			si := &siList.Items[i]
			if strings.HasPrefix(si.Name, siNamePrefix) {
				if si.Spec.IPs[0] == serviceIP {
					retServiceImport = &siList.Items[i]
					return true, "", nil
				}
			}
		}

		return false, fmt.Sprintf("Failed to find ServiceImport with IP %s", serviceIP), nil
	})

	return retServiceImport
}

func (f *Framework) AwaitServiceImportDelete(targetCluster framework.ClusterIndex, name, namespace string) {
	siNamePrefix := name + "-" + namespace
	si := MCSClients[targetCluster].MulticlusterV1alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		return si.List(context.TODO(), metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		for i := range siList.Items {
			si := &siList.Items[i]
			if strings.HasPrefix(si.Name, siNamePrefix) {
				return false, fmt.Sprintf("ServiceImport with name prefix %s still exists", siNamePrefix), nil
			}
		}

		return true, "", nil
	})
}

func (f *Framework) AwaitServiceImportCount(targetCluster framework.ClusterIndex, name, namespace string, count int) {
	labelMap := map[string]string{
		labelSourceName:      name,
		labelSourceNamespace: namespace,
	}
	siListOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelMap).String(),
	}
	si := MCSClients[targetCluster].MulticlusterV1alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		return si.List(context.TODO(), siListOptions)
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		if len(siList.Items) != count {
			return false, fmt.Sprintf("ServiceImport count was %v instead of %v", len(siList.Items), count), nil
		}

		return true, "", nil
	})
}

func (f *Framework) NewNginxHeadlessServiceWithParams(name, app, portName string, protcol v1.Protocol,
	cluster framework.ClusterIndex) *v1.Service {
	var port int32 = 80

	nginxService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app": app,
			},
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
			Selector: map[string]string{
				"app": app,
			},
		},
	}

	sc := framework.KubeClients[cluster].CoreV1().Services(f.Namespace)
	service := framework.AwaitUntil("create service", func() (interface{}, error) {
		return sc.Create(context.TODO(), &nginxService, metav1.CreateOptions{})
	}, framework.NoopCheckResult).(*v1.Service)

	return service
}

func (f *Framework) NewNginxHeadlessService(cluster framework.ClusterIndex) *v1.Service {
	return f.NewNginxHeadlessServiceWithParams("nginx-headless", "nginx-demo", "http", v1.ProtocolTCP, cluster)
}

func (f *Framework) AwaitEndpointIPs(targetCluster framework.ClusterIndex, name,
	namespace string, count int) (ipList, hostNameList []string) {
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

func (f *Framework) AwaitPodIngressIPs(targetCluster framework.ClusterIndex, svc *v1.Service, count int) (ipList, hostNameList []string) {
	podList := f.Framework.AwaitPodsByAppLabel(targetCluster, svc.Labels["app"], svc.Namespace, count)
	hostNameList = make([]string, 0)
	ipList = make([]string, 0)

	for i := 0; i < len(podList.Items); i++ {
		ingressIPName := fmt.Sprintf("pod-%s", podList.Items[i].Name)
		ingressIP := f.Framework.AwaitGlobalIngressIP(targetCluster, ingressIPName, svc.Namespace)
		ipList = append(ipList, ingressIP)

		hostname := podList.Items[i].Spec.Hostname
		if hostname == "" {
			hostname = podList.Items[i].Name
		}

		hostNameList = append(hostNameList, hostname)
	}

	return ipList, hostNameList
}

func (f *Framework) AwaitPodIPs(targetCluster framework.ClusterIndex, svc *v1.Service, count int) (ipList, hostNameList []string) {
	if framework.TestContext.GlobalnetEnabled {
		return f.AwaitPodIngressIPs(targetCluster, svc, count)
	}

	return f.AwaitEndpointIPs(targetCluster, svc.Name, svc.Namespace, count)
}

func (f *Framework) GetPodIPs(targetCluster framework.ClusterIndex, service *v1.Service) (ipList, hostNameList []string) {
	return f.AwaitPodIPs(targetCluster, service, anyCount)
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
							Image:           "quay.io/bitnami/nginx:latest",
							ImagePullPolicy: v1.PullAlways,
							Ports: []v1.ContainerPort{
								{
									ContainerPort: port,
									Name:          "web",
								},
							},
							Command: []string{},
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
	expSliceCount, expEpCount int) (endpointSliceList *v1beta1.EndpointSliceList) {
	ep := framework.KubeClients[targetCluster].DiscoveryV1beta1().EndpointSlices(namespace)
	labelMap := map[string]string{
		v1beta1.LabelManagedBy: lhconstants.LabelValueManagedBy,
	}
	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelMap).String(),
	}

	By(fmt.Sprintf("Retrieving EndpointSlices for %q in ns %q on %q", name, namespace,
		framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve EndpointSlices", func() (interface{}, error) {
		return ep.List(context.TODO(), listOptions)
	}, func(result interface{}) (bool, string, error) {
		endpointSliceList = result.(*v1beta1.EndpointSliceList)
		sliceCount := 0
		epCount := 0

		for i := range endpointSliceList.Items {
			es := &endpointSliceList.Items[i]
			if name == "" || strings.HasPrefix(es.Name, name) {
				sliceCount++
				epCount += len(es.Endpoints)
			}
		}

		if expSliceCount != anyCount && sliceCount != expSliceCount {
			return false, fmt.Sprintf("%d EndpointSlices found when expected %d", len(endpointSliceList.Items), expSliceCount), nil
		}

		if expEpCount != anyCount && epCount != expEpCount {
			return false, fmt.Sprintf("%d total Endpoints found when expected %d", epCount, expEpCount), nil
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
	domains []string, clusterName string, shouldContain bool) {
	serviceIP := f.GetServiceIP(targetCluster, service, srcCluster == targetCluster)
	f.VerifyIPWithDig(srcCluster, service, targetPod, domains, clusterName, serviceIP, shouldContain)
}

func (f *Framework) VerifyIPWithDig(srcCluster framework.ClusterIndex, service *v1.Service, targetPod *v1.PodList,
	domains []string, clusterName, serviceIP string, shouldContain bool) {
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
		stdout, _, err := f.ExecWithOptions(&framework.ExecOptions{
			Command:       cmd,
			Namespace:     f.Namespace,
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

func (f *Framework) GetServiceIP(svcCluster framework.ClusterIndex, service *v1.Service, isLocal bool) string {
	Expect(service.Spec.Type).To(Equal(v1.ServiceTypeClusterIP))

	if !framework.TestContext.GlobalnetEnabled || isLocal {
		return service.Spec.ClusterIP
	}

	return f.Framework.AwaitGlobalIngressIP(svcCluster, service.Name, service.Namespace)
}
