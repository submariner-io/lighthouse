package lighthouse

import (
	"fmt"

	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	lighthouseClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	lighthouseInformers "github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type DNSController struct {
	// Indirection hook for unit tests to supply fake client sets
	newClientset         func(kubeConfig *rest.Config) (lighthouseClientset.Interface, error)
	serviceInformer      cache.SharedIndexInformer
	queue                workqueue.RateLimitingInterface
	stopCh               chan struct{}
	multiClusterServices *multiClusterServiceMap
}

func newController(remoteServiceMap *multiClusterServiceMap) *DNSController {

	return &DNSController{
		queue: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		newClientset: func(c *rest.Config) (lighthouseClientset.Interface, error) {
			return lighthouseClientset.NewForConfig(c)

		},
		multiClusterServices: remoteServiceMap,
		stopCh:               make(chan struct{}),
	}
}

func (c *DNSController) start(kubeConfig *rest.Config) error {
	klog.Infof("Starting DNSController")

	clientSet, err := c.newClientset(kubeConfig)
	if err != nil {
		return fmt.Errorf("Error creating client set: %v", err)
	}

	informerFactory := lighthouseInformers.NewSharedInformerFactoryWithOptions(clientSet, 0,
		lighthouseInformers.WithNamespace(metav1.NamespaceAll))

	c.serviceInformer = informerFactory.Lighthouse().V1().MultiClusterServices().Informer()
	c.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.V(2).Infof("MultiClusterService %q added", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.V(2).Infof("MultiClusterService %q updated", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.V(2).Infof("MultiClusterService %q deleted", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
	})

	go c.serviceInformer.Run(c.stopCh)
	go c.runWorker()

	return nil
}

func (c *DNSController) stop() {
	close(c.stopCh)
	c.queue.ShutDown()

	klog.Infof(" DNSController stopped")
}

func (c *DNSController) runWorker() {
	for {
		keyObj, shutdown := c.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for MultiClusterServices stopped")
			return
		}

		key := keyObj.(string)
		func() {
			defer c.queue.Done(key)
			obj, exists, err := c.serviceInformer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving service with key %q from the cache: %v", key, err)
				// requeue the item to work on later
				c.queue.AddRateLimited(key)
				return
			}

			if !exists {
				c.multiClusterServiceDeleted(key)
			} else {
				c.multiClusterServiceCreatedOrUpdated(obj, key)
			}

			c.queue.Forget(key)
		}()
	}
}

func (c *DNSController) multiClusterServiceCreatedOrUpdated(obj interface{}, key string) {
	klog.V(2).Infof("In multiClusterServiceCreatedOrUpdated for key %q, service: %#v, ", key, obj)

	c.multiClusterServices.put(obj.(*lighthousev1.MultiClusterService))
}

func (c *DNSController) multiClusterServiceDeleted(key string) {
	klog.V(2).Infof("In multiClusterServiceDeleted for key %q", key)

	namespace, name, _ := cache.SplitMetaNamespaceKey(key)
	c.multiClusterServices.remove(namespace, name)
}
