package framework

import (
	"fmt"
	"strings"

	"k8s.io/client-go/dynamic"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lhconstants "github.com/submariner-io/lighthouse/pkg/constants"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/mcs/client/clientset/versioned"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	labelSourceName        = "lighthouse.submariner.io/sourceName"
	labelSourceNamespace   = "lighthouse.submariner.io/sourceNamespace"
	anyCount               = -1
	statefulServiceName    = "nginx-ss"
	statefulSetName        = "web"
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
type Framework struct {
	*framework.Framework
}

var MCSClients []*mcsClientset.Clientset
var EndpointClients []dynamic.ResourceInterface

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
	_, err = endpointsClient.List(metav1.ListOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return endpointsClient
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
		return se.Create(&nginxServiceExport)
	}, framework.NoopCheckResult).(*mcsv1a1.ServiceExport)

	return serviceExport
}

func (f *Framework) AwaitServiceExportedStatusCondition(cluster framework.ClusterIndex, name, namespace string) {
	se := MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace)
	By(fmt.Sprintf("Retrieving ServiceExport %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	framework.AwaitUntil("retrieve ServiceExport", func() (interface{}, error) {
		return se.Get(name, metav1.GetOptions{})
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
		return nil, MCSClients[cluster].MulticlusterV1alpha1().ServiceExports(namespace).Delete(name, &metav1.DeleteOptions{})
	}, framework.NoopCheckResult)
}

func (f *Framework) GetService(cluster framework.ClusterIndex, name, namespace string) (*v1.Service, error) {
	By(fmt.Sprintf("Retrieving service %s.%s on %q", name, namespace, framework.TestContext.ClusterIDs[cluster]))
	return framework.KubeClients[cluster].CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

func (f *Framework) AwaitServiceImportIP(targetCluster framework.ClusterIndex, svc *v1.Service) *mcsv1a1.ServiceImport {
	var serviceIP string

	if framework.TestContext.GlobalnetEnabled {
		serviceIP = svc.Annotations[submarinerIpamGlobalIp]
	} else {
		serviceIP = svc.Spec.ClusterIP
	}

	var retServiceImport *mcsv1a1.ServiceImport

	siNamePrefix := svc.Name + "-" + svc.Namespace + "-"
	si := MCSClients[targetCluster].MulticlusterV1alpha1().ServiceImports(framework.TestContext.SubmarinerNamespace)
	By(fmt.Sprintf("Retrieving ServiceImport for %s on %q", siNamePrefix, framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve ServiceImport", func() (interface{}, error) {
		return si.List(metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		if len(siList.Items) < 1 {
			return false, fmt.Sprintf("ServiceImport with name prefix %s not found", siNamePrefix), nil
		}
		for i, si := range siList.Items {
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
		return si.List(metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		for _, si := range siList.Items {
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
		return si.List(siListOptions)
	}, func(result interface{}) (bool, string, error) {
		siList := result.(*mcsv1a1.ServiceImportList)
		if len(siList.Items) != count {
			return false, fmt.Sprintf("ServiceImport count was %v instead of %v", len(siList.Items), count), nil
		}

		return true, "", nil
	})
}

func (f *Framework) AwaitGlobalnetIP(cluster framework.ClusterIndex, name, namespace string) string {
	if framework.TestContext.GlobalnetEnabled {
		svc := framework.KubeClients[cluster].CoreV1().Services(namespace)
		svcObj := framework.AwaitUntil("retrieve service", func() (interface{}, error) {
			return svc.Get(name, metav1.GetOptions{})
		}, func(result interface{}) (bool, string, error) {
			svc := result.(*v1.Service)
			globalIp := svc.Annotations[submarinerIpamGlobalIp]
			if globalIp == "" {
				return false, "GlobalIP not available", nil
			}
			return true, "", nil
		}).(*v1.Service)

		return svcObj.Annotations[submarinerIpamGlobalIp]
	}

	return ""
}

func (f *Framework) NewNginxHeadlessServiceWithParams(name, app string, cluster framework.ClusterIndex) *v1.Service {
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
					Port: port,
				},
			},
			Selector: map[string]string{
				"app": app,
			},
		},
	}

	sc := framework.KubeClients[cluster].CoreV1().Services(f.Namespace)
	service := framework.AwaitUntil("create service", func() (interface{}, error) {
		return sc.Create(&nginxService)
	}, framework.NoopCheckResult).(*v1.Service)

	return service
}

func (f *Framework) NewNginxHeadlessService(cluster framework.ClusterIndex) *v1.Service {
	return f.NewNginxHeadlessServiceWithParams("nginx-headless", "nginx-demo", cluster)
}

func (f *Framework) AwaitEndpointIPs(targetCluster framework.ClusterIndex, name, namespace string, count int) (ipList []string) {
	ep := framework.KubeClients[targetCluster].CoreV1().Endpoints(namespace)
	By(fmt.Sprintf("Retrieving Endpoints for %s on %q", name, framework.TestContext.ClusterIDs[targetCluster]))
	framework.AwaitUntil("retrieve Endpoints", func() (interface{}, error) {
		return ep.Get(name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		ipList = make([]string, 0)
		endpoint := result.(*v1.Endpoints)
		for _, eps := range endpoint.Subsets {
			for _, addr := range eps.Addresses {
				ipList = append(ipList, addr.IP)
			}
		}
		if count != anyCount && len(ipList) != count {
			return false, fmt.Sprintf("endpoints have %d IPs when expected %d", len(ipList), count), nil
		}
		return true, "", nil
	})

	return ipList
}

func (f *Framework) GetEndpointIPs(targetCluster framework.ClusterIndex, name, namespace string) (ipList []string) {
	return f.AwaitEndpointIPs(targetCluster, name, namespace, anyCount)
}

func (f *Framework) SetNginxReplicaSet(cluster framework.ClusterIndex, count uint32) *appsv1.Deployment {
	By(fmt.Sprintf("Setting Nginx deployment replicas to %v in cluster %q", count, framework.TestContext.ClusterIDs[cluster]))
	patch := fmt.Sprintf(`{"spec":{"replicas":%v}}`, count)
	deployments := framework.KubeClients[cluster].AppsV1().Deployments(f.Namespace)
	result := framework.AwaitUntil("set replicas", func() (interface{}, error) {
		return deployments.Patch("nginx-demo", types.MergePatchType, []byte(patch))
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
		return pc.Create(statefulSet)
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
		return ep.List(listOptions)
	}, func(result interface{}) (bool, string, error) {
		endpointSliceList = result.(*v1beta1.EndpointSliceList)
		sliceCount := 0
		epCount := 0

		for _, es := range endpointSliceList.Items {
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
		return ss.Patch(statefulSetName, types.MergePatchType, []byte(patch))
	}, framework.NoopCheckResult).(*appsv1.StatefulSet)

	return result
}

func (f *Framework) GetHealthCheckIPInfo(cluster framework.ClusterIndex) (endpointName, healthCheckIP string) {
	framework.AwaitUntil("Get healthCheckIP", func() (interface{}, error) {
		unstructuredEndpointList, err := EndpointClients[cluster].List(metav1.ListOptions{})
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

func (f *Framework) SetHealthCheckIP(cluster framework.ClusterIndex, ip, endpointName string) {
	By(fmt.Sprintf("Setting health check IP cluster %q to %v", framework.TestContext.ClusterIDs[cluster], ip))
	patch := fmt.Sprintf(`{"spec":{"healthCheckIP":%q}}`, ip)

	framework.AwaitUntil("set healthCheckIP", func() (interface{}, error) {
		endpoint, err := EndpointClients[cluster].Patch(endpointName, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
		return endpoint, err
	}, framework.NoopCheckResult)
}
