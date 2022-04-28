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

	"github.com/submariner-io/shipyard/test/e2e/framework"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) NewNetShootDeployment(cluster framework.ClusterIndex) *corev1.PodList {
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
							Image:           "quay.io/submariner/nettest:devel",
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

	return createDeployment(f, cluster, netShootDeployment)
}

func (f *Framework) NewNginxDeployment(cluster framework.ClusterIndex) *corev1.PodList {
	var replicaCount int32 = 1
	var port int32 = 8080
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
							Image:           "quay.io/submariner/nettest:devel",
							ImagePullPolicy: corev1.PullAlways,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: port,
								},
							},
							Command: []string{
								"/app/simpleserver",
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}

	return createDeployment(f, cluster, nginxDeployment)
}

func createDeployment(f *Framework, cluster framework.ClusterIndex, deployment *appsv1.Deployment) *corev1.PodList {
	pc := framework.KubeClients[cluster].AppsV1().Deployments(f.Namespace)
	appName := deployment.Spec.Template.ObjectMeta.Labels["app"]

	_ = framework.AwaitUntil("create deployment", func() (interface{}, error) {
		return pc.Create(context.TODO(), deployment, metav1.CreateOptions{})
	}, framework.NoopCheckResult).(*appsv1.Deployment)

	return f.AwaitPodsByAppLabel(cluster, appName, f.Namespace, 1)
}
