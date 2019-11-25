package framework

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Deployment struct {
	PodList   *v1.PodList
	framework *Framework
	cluster   ClusterIndex
	serviceIP string
}

func (f *Framework) NewNetShootDeployment(Cluster ClusterIndex, ServiceIP string) *Deployment {
	deployment := &Deployment{framework: f, cluster: Cluster, serviceIP: ServiceIP}
	deployment.buildNetshootDeployment()
	return deployment
}

func (f *Framework) NewNginxDeployment(Cluster ClusterIndex) *Deployment {
	deployment := &Deployment{framework: f, cluster: Cluster}
	deployment.buildNginxDeployment()
	return deployment
}

func (d *Deployment) AwaitReady() {
	pods := d.framework.ClusterClients[d.cluster].CoreV1().Pods(d.framework.Namespace)
	d.PodList, _ = pods.List(metav1.ListOptions{})
	d.PodList = AwaitUntil("get pod", func() (interface{}, error) {
		podList, err := pods.List(metav1.ListOptions{})
		if nil != err {
			Logf("Error while retrieving podlist")
			return nil, err
		}
		return podList, nil
	}, func(result interface{}) (bool, error) {
		podList := result.(*v1.PodList)
		if len(podList.Items) == 0 {
			return false, nil
		}
		for _, pod := range podList.Items {
			if pod.Status.Phase != v1.PodRunning {
				if pod.Status.Phase != v1.PodPending {
					return false, fmt.Errorf("unexpected pod phase %v - expected %v or %v",
						pod.Status.Phase, v1.PodPending, v1.PodRunning)
				}
				return false, nil // pod is still pending
			}
		}
		return true, nil // pod is running
	}).(*v1.PodList)
}

func (d *Deployment) buildNetshootDeployment() {
	var replicaCount int32 = 1
	netShootDeployment := appsv1.Deployment{
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

	pc := d.framework.ClusterClients[d.cluster].AppsV1().Deployments(d.framework.Namespace)

	_ = AwaitUntil("create deployment", func() (interface{}, error) {
		return pc.Create(&netShootDeployment)

	}, NoopCheckResult).(*appsv1.Deployment)
	d.AwaitReady()
}

func (d *Deployment) buildNginxDeployment() {
	var replicaCount int32 = 1
	var port int32 = 80
	nginxDeployment := appsv1.Deployment{
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

	pc := d.framework.ClusterClients[d.cluster].AppsV1().Deployments(d.framework.Namespace)

	_ = AwaitUntil("create deployment", func() (interface{}, error) {
		return pc.Create(&nginxDeployment)

	}, NoopCheckResult).(*appsv1.Deployment)
	d.AwaitReady()
}
