package controller

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const (
	submarinerIpamGlobalIp = "submariner.io/globalIp"
)

var excludeSvcList = map[string]bool{"kubernetes": true, "openshift": true}

func New(spec *AgentSpecification, config InformerConfigStruct) *Controller {
	exclusionNSMap := map[string]bool{"kube-system": true, "submariner": true, "openshift": true,
		"submariner-operator": true, "kubefed-operator": true}
	for _, v := range spec.ExcludeNS {
		exclusionNSMap[v] = true
	}
	agentController := &Controller{
		kubeClientSet:    config.KubeClientSet,
		serviceWorkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Services"),
		servicesSynced:   config.ServiceInformer.Informer().HasSynced,

		excludeNamespaces: exclusionNSMap,
	}

	klog.Info("Setting up event handlers...")
	config.ServiceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: agentController.enqueueService,
		UpdateFunc: func(old, newObj interface{}) {
			agentController.handleUpdateService(old, newObj)
		},
		DeleteFunc: agentController.handleRemovedService,
	})

	return agentController
}

func (a *Controller) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Agent controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, a.servicesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	go wait.Until(a.runServiceWorker, time.Second, stopCh)
	klog.Info("Lighthouse agent workers started")
	<-stopCh
	klog.Info("Lighthouse Agent stopping")
	return nil
}

func (a *Controller) enqueueService(obj interface{}) {
	if key := a.getEnqueueKey(obj); key != "" {
		klog.V(4).Infof("Enqueueing service %v for agent", key)
		a.serviceWorkqueue.AddRateLimited(key)
	}
}

func (a *Controller) getEnqueueKey(obj interface{}) string {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return ""
	}
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(err)
		return ""
	}
	if !a.isValidService(ns, name) {
		return ""
	}
	return key
}

func (a *Controller) isValidService(namespace string, serviceName string) bool {
	if a.excludeNamespaces[namespace] || excludeSvcList[serviceName] {
		return false
	}
	return true
}

func (a *Controller) runServiceWorker() {
	for a.processNextService() {

	}
}

func (a *Controller) processNextService() bool {
	obj, shutdown := a.serviceWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer a.serviceWorkqueue.Done(obj)

		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			a.serviceWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Retrieve the latest version of object before attempting update
			// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			service, err := a.kubeClientSet.CoreV1().Services(ns).Get(name, metav1.GetOptions{})

			if err != nil {
				if errors.IsNotFound(err) {
					// already deleted Forget and return
					return nil
				}
				return fmt.Errorf("error retrieving service %s: %v", name, err)
			}

			return a.serviceUpdater(service, key)
		})

		if retryErr != nil {
			logAndRequeue(key, a.serviceWorkqueue)
		}

		a.serviceWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (a *Controller) handleUpdateService(old interface{}, newObj interface{}) {
	// ServiceIp can't change without deleting service, so we only check for globalIp change
	if key := a.getEnqueueKey(newObj); key != "" {
		oldGlobalIp := getGlobalIpFromService(old.(*corev1.Service))
		newGlobalIp := getGlobalIpFromService(newObj.(*corev1.Service))
		if oldGlobalIp != newGlobalIp {
			a.serviceWorkqueue.AddRateLimited(key)
		}
	}
}

func (a *Controller) serviceUpdater(service *corev1.Service, key string) error {
	klog.V(2).Infof("Updating Service %s", key)
	return nil
}

func (a *Controller) handleRemovedService(obj interface{}) {
	var service *corev1.Service
	var ok bool

	if service, ok = obj.(*corev1.Service); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %v to Service", obj)
			return
		}
		service, ok = tombstone.Obj.(*corev1.Service)
		if !ok {
			klog.Errorf("Could not convert object tombstone %v to Service", tombstone.Obj)
			return
		}
	}
	if !a.isValidService(service.GetNamespace(), service.GetName()) {
		return
	}
	klog.V(2).Infof("Removed service %v", service)
}

func logAndRequeue(key string, workqueue workqueue.RateLimitingInterface) {
	klog.V(2).Infof("%s enqueued %d times", key, workqueue.NumRequeues(key))
	workqueue.AddRateLimited(key)
}

func getGlobalIpFromService(service *corev1.Service) string {
	if service != nil {
		annotations := service.GetAnnotations()
		if annotations != nil {
			return annotations[submarinerIpamGlobalIp]
		}
	}
	return ""
}
