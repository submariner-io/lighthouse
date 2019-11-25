package framework

import (
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type ServiceType int

const (
	InvalidServicePodType ServiceType = iota
	NginxService
)

type Service struct {
	Service   *corev1.Service
	framework *Framework
	Cluster   ClusterIndex
}

func (f *Framework) NewNginxService(Cluster ClusterIndex) *Service {

	service := &Service{framework: f, Cluster: Cluster}
	service.buildNginxService()
	return service
}

func (s *Service) buildNginxService() {
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

	sc := s.framework.ClusterClients[s.Cluster].CoreV1().Services(s.framework.Namespace)
	s.Service = AwaitUntil("create service", func() (interface{}, error) {
		return sc.Create(&nginxService)

	}, NoopCheckResult).(*v1.Service)
}
