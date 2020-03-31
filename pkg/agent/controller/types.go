package controller

import (
	kubeInformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type Controller struct {
	kubeClientSet     kubernetes.Interface
	serviceWorkqueue  workqueue.RateLimitingInterface
	servicesSynced    cache.InformerSynced
	excludeNamespaces map[string]bool
}

type AgentSpecification struct {
	ClusterID string
	Namespace string
	Token     string
	Broker    string
	ExcludeNS []string
}

type InformerConfigStruct struct {
	KubeClientSet   kubernetes.Interface
	ServiceInformer kubeInformers.ServiceInformer
}
