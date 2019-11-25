package framework

import (
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeploymentType int

const (
	InvalidDeploymentPodType DeploymentType = iota
	NginxDeployment
	NetShootDeployment
)

type DeploymentConfig struct {
	Type         DeploymentType
	Cluster      ClusterIndex
	ServiceIP    string
	ReplicaCount int32
	PodName      string
	// TODO: namespace, once https://github.com/submariner-io/submariner/pull/141 is merged
}
type Deployment struct {
	PodList            *v1.PodList
	Deployment         *appsv1.Deployment
	Config             *DeploymentConfig
	TerminationCode    int32
	TerminationMessage string
	framework          *Framework
}

func (f *Framework) NewDeployment(config *DeploymentConfig) *Deployment {

	// check if all necessary details are provided
	Expect(config.Type).ShouldNot(Equal(InvalidDeploymentPodType))

	deployment := &Deployment{Config: config, framework: f, TerminationCode: -1}

	switch config.Type {
	case NetShootDeployment:
		deployment.buildNetshootDeployment()
	case NginxDeployment:
		deployment.buildNginxDeployment()
	}

	return deployment
}

func (d *Deployment) AwaitReady() {
	pods := d.framework.ClusterClients[d.Config.Cluster].CoreV1().Pods(d.framework.Namespace)
	d.PodList, _ = pods.List(metav1.ListOptions{})
	d.PodList = AwaitUntil("get pod", func() (interface{}, error) {
		podList, err := pods.List(metav1.ListOptions{})
		if nil != err {
			Logf("Error while retrieving podlist")
			return nil, err
		}
		d.PodList = podList
		return podList, nil
	}, func(result interface{}) (bool, error) {
		if d.PodList == nil || len(d.PodList.Items) == 0 {
			return false, nil
		}
		for _, pod := range d.PodList.Items {
			if pod.Status.Phase != v1.PodRunning {
				if pod.Status.Phase != v1.PodPending {
					return false, nil
				}
				return false, nil // pod is still pending
			}
		}
		return true, nil // pod is running
	}).(*v1.PodList)
}

func (d *Deployment) AwaitSuccessfulFinish() {
	pods := d.framework.ClusterClients[d.Config.Cluster].CoreV1().Pods(d.framework.Namespace)

	d.PodList = AwaitUntil("get pod", func() (interface{}, error) {
		podList, err := pods.List(metav1.ListOptions{})
		if nil != err {
			Logf("Error while retrieving podlist")
			return nil, err
		}
		d.PodList = podList
		return podList, nil
	}, func(result interface{}) (bool, error) {
		if d.PodList.Items == nil {
			Logf("no pods present")
			return false, nil
		}
		if d.PodList == nil || len(d.PodList.Items) == 0 {
			return false, nil
		}
		for _, pod := range d.PodList.Items {
			Logf("pod.Status.Phase %v", pod.Status.Phase)
			switch pod.Status.Phase {
			case v1.PodRunning:
				Logf("Continue")
				continue
			default:
				Logf("Returning false %v", pod.Status.Phase)
				return false, nil
			}
		}
		Logf("Return default true")
		return true, nil
	}).(*v1.PodList)
}

func (d *Deployment) buildNetshootDeployment() {
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
			Replicas: &d.Config.ReplicaCount,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "netshoot",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            d.Config.PodName,
							Image:           "nicolaka/netshoot",
							ImagePullPolicy: corev1.PullAlways,
							/*Command: []string{
								"sh", "-c", "curl -m 30  --head --fail nginx-demo >/dev/termination-log; sleep 600",
							},*/
							Command: []string{
								"sleep", "600",
							},
							Env: []corev1.EnvVar{
								{Name: "SERVICE_IP", Value: d.Config.ServiceIP},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}

	pc := d.framework.ClusterClients[d.Config.Cluster].AppsV1().Deployments(d.framework.Namespace)
	var err error
	attempts := 5
	for ; attempts > 0; attempts-- {
		d.Deployment, err = pc.Create(&netShootDeployment)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
	Expect(err).NotTo(HaveOccurred())
	d.AwaitReady()
}

func (d *Deployment) buildNginxDeployment() {
	var replicas int32 = 2
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
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx-demo",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            d.Config.PodName,
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

	pc := d.framework.ClusterClients[d.Config.Cluster].AppsV1().Deployments(d.framework.Namespace)
	var err error
	attempts := 5
	for ; attempts > 0; attempts-- {
		d.Deployment, err = pc.Create(&nginxDeployment)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 500)
	}

	Expect(err).NotTo(HaveOccurred())
	d.AwaitReady()
}
