package framework

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type ServiceType int

func (f *Framework) NewNginxService(cluster ClusterIndex) *corev1.Service {
	var port int32 = 80
	nginxService := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nginx-demo",
			Labels: map[string]string{
				"app": "nginx-demo",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: "ClusterIP",
			Ports: []corev1.ServicePort{
				{
					Port:     port,
					Protocol: corev1.ProtocolTCP,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 80,
					},
				},
			},
			Selector: map[string]string{
				"app": "nginx-demo",
			},
		},
	}

	sc := f.ClusterClients[cluster].CoreV1().Services(f.Namespace)
	service := AwaitUntil("create service", func() (interface{}, error) {
		return sc.Create(&nginxService)

	}, NoopCheckResult).(*v1.Service)
	return service
}

func (f *Framework) DeleteService(cluster ClusterIndex, serviceName string) {
	By(fmt.Sprintf("Deleting service %q on %q", serviceName, TestContext.KubeContexts[cluster]))
	AwaitUntil("delete service", func() (interface{}, error) {
		return nil, f.ClusterClients[cluster].CoreV1().Services(f.Namespace).Delete(serviceName, &metav1.DeleteOptions{})
	}, NoopCheckResult)
}
