package framework

import (
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type ServiceType int

const (
	InvalidServicePodType ServiceType = iota
	NginxService
)

type ServiceConfig struct {
	Type    ServiceType
	Cluster ClusterIndex
	// TODO: namespace, once https://github.com/submariner-io/submariner/pull/141 is merged
}
type Service struct {
	Service            *corev1.Service
	Config             *ServiceConfig
	TerminationCode    int32
	TerminationMessage string
	framework          *Framework
}

func (f *Framework) NewService(config *ServiceConfig) *Service {

	// check if all necessary details are provided
	Expect(config.Type).ShouldNot(Equal(InvalidServicePodType))

	service := &Service{Config: config, framework: f, TerminationCode: -1}

	switch config.Type {
	case NginxService:
		service.buildNginxService()
	}
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

	sc := s.framework.ClusterClients[s.Config.Cluster].CoreV1().Services(s.framework.Namespace)
	var err error
	attempts := 5
	for ; attempts > 0; attempts-- {
		s.Service, err = sc.Create(&nginxService)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
	Expect(err).NotTo(HaveOccurred())
}
