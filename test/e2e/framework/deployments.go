package framework

import (
	"github.com/submariner-io/submariner/test/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) NewNetShootDeployment(cluster framework.ClusterIndex) *v1.PodList {
	var replicaCount int32 = 1
	netShootDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "netshoot",
			Labels: map[string]string{
				"run": "netshoot",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "netshoot",
				},
			},
			Replicas: &replicaCount,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "netshoot",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "netshoot",
							Image:           "nicolaka/netshoot",
							ImagePullPolicy: corev1.PullAlways,
							Command: []string{
								"sleep", "600",
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}

	return create(f, cluster, netShootDeployment)
}

func (f *Framework) NewNginxDeployment(cluster framework.ClusterIndex) *v1.PodList {
	var replicaCount int32 = 1
	var port int32 = 80
	nginxDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nginx-demo",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx-demo",
				},
			},
			Replicas: &replicaCount,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx-demo",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "nginx-demo",
							Image:           "nginx:alpine",
							ImagePullPolicy: corev1.PullAlways,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: port,
								},
							},
							Command: []string{},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}

	return create(f, cluster, nginxDeployment)
}

func create(f *Framework, cluster framework.ClusterIndex, deployment *appsv1.Deployment) *v1.PodList {
	pc := f.ClusterClients[cluster].AppsV1().Deployments(f.Namespace)
	appName := deployment.Spec.Template.ObjectMeta.Labels["app"]

	_ = framework.AwaitUntil("create deployment", func() (interface{}, error) {
		return pc.Create(deployment)
	}, framework.NoopCheckResult).(*appsv1.Deployment)

	return f.Framework.AwaitPodsByAppLabel(cluster, appName, f.Namespace, 1)
}
