package framework

import (
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PodType int

const (
	InvalidPodType PodType = iota
	DnsToolsPod
)

type PodConfig struct {
	Type      PodType
	Cluster   ClusterIndex
	ServiceIP string
	// TODO: namespace, once https://github.com/submariner-io/submariner/pull/141 is merged
}
type Pod struct {
	Pod                *corev1.Pod
	Config             *PodConfig
	TerminationCode    int32
	TerminationMessage string
	framework          *Framework
}

func (f *Framework) NewPod(config *PodConfig) *Pod {

	// check if all necessary details are provided
	Expect(config.Type).ShouldNot(Equal(InvalidPodType))

	servicePod := &Pod{Config: config, framework: f, TerminationCode: -1}

	switch config.Type {
	case DnsToolsPod:
		servicePod.buildDnsToolsPod()
	}

	return servicePod
}

// create a test pod inside the current test namespace on the specified cluster.
// The pod will connect to remoteIP:TestPort over TCP, send sendString over the
// connection, and write the network response in the pod termination log, then
// exit with 0 status
func (p *Pod) buildDnsToolsPod() {

	tcpCheckConnectorPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "dnstools",
			Labels: map[string]string{
				"run": "dnstools",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:                     "dnstools",
					Image:                    "infoblox/dnstools:latest",
					ImagePullPolicy:          corev1.PullAlways,
					Stdin:                    true,
					StdinOnce:                true,
					TerminationMessagePath:   corev1.TerminationMessagePathDefault,
					TerminationMessagePolicy: corev1.TerminationMessageReadFile,
					TTY:                      true,
				},
			},
			DNSPolicy:                     corev1.DNSClusterFirst,
			EnableServiceLinks:            func() *bool { i := bool(true); return &i }(),
			Priority:                      func() *int32 { i := int32(0); return &i }(),
			SchedulerName:                 corev1.DefaultSchedulerName,
			ServiceAccountName:            "default",
			TerminationGracePeriodSeconds: func() *int64 { i := int64(corev1.DefaultTerminationGracePeriodSeconds); return &i }(),
		},
	}

	pc := p.framework.ClusterClients[p.Config.Cluster].CoreV1().Pods(p.framework.Namespace)
	var err error
	p.Pod, err = pc.Create(&tcpCheckConnectorPod)
	Expect(err).NotTo(HaveOccurred())
}
