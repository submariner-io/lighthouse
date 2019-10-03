package lighthouse

import (
	"sync"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/client-go/rest"

	mcservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	mcsClientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	mcsinformer "github.com/submariner-io/lighthouse/pkg/client/informers/externalversions"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

type DNSController struct {
	dnsControllerMutex *sync.Mutex
	// Indirection hook for unit tests to supply fake client sets
	newClientset     func(kubeConfig *rest.Config) (mcsClientset.Interface, error)
	serviceInformer  cache.SharedIndexInformer
	queue            workqueue.RateLimitingInterface
	stopCh           chan struct{}
	remoteServiceMap remoteServiceMap
}

func New(remoteServiceMap remoteServiceMap) *DNSController {

	return &DNSController{
		dnsControllerMutex: &sync.Mutex{},
		queue:              workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		newClientset: func(c *rest.Config) (mcsClientset.Interface, error) {
			return mcsClientset.NewForConfig(c)

		},
		remoteServiceMap: remoteServiceMap,
		stopCh:           make(chan struct{}),
	}
}

func (c *DNSController) Start(kubeConfig *rest.Config) error {
	klog.Infof("Starting DNSController")
	mcsClient, err := c.newClientset(kubeConfig)
	if err != nil {
		klog.Errorf("Error creating client set: %v", err)
		return err
	}

	mcsInformerFactory := mcsinformer.NewSharedInformerFactoryWithOptions(mcsClient, 0,
		mcsinformer.WithNamespace(meta_v1.NamespaceAll))

	mcsV1 := mcsInformerFactory.Lighthouse().V1()
	clusterInformer := mcsV1.MultiClusterServices().Informer()
	clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			klog.V(2).Infof("MulitClusterService %q added", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		UpdateFunc: func(obj interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			klog.V(2).Infof("MulitClusterService %q updated", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			klog.V(2).Infof("MulitClusterService %q deleted", key)
			if err == nil {
				c.queue.Add(key)
			}
		},
	})

	c.serviceInformer = clusterInformer
	go c.run()
	return nil
}

func (c *DNSController) Stop() {
	c.dnsControllerMutex.Lock()
	defer c.dnsControllerMutex.Unlock()
	close(c.stopCh)
	c.queue.ShutDown()
	klog.Infof(" DNSController watcher queue shutdown")
}

func (c *DNSController) run() {
	defer utilruntime.HandleCrash()
	go c.serviceInformer.Run(c.stopCh)
	go c.runWorker()
}

func (c *DNSController) runWorker() {
	for {
		keyObj, shutdown := c.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for cluster stopped")
			return
		}

		key := keyObj.(string)
		func() {
			defer c.queue.Done(key)
			obj, exists, err := c.serviceInformer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("error processing %s (will retry): %v", key, err)
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
	klog.V(2).Infof("MultiClusterServiceCreatedOrUpdated %q created", key)
	service := obj.(*mcservice.MultiClusterService)
	c.dnsControllerMutex.Lock()
	defer c.dnsControllerMutex.Unlock()
	c.remoteServiceMap[key] = service
}

// ObjectDeleted is called when an object is deleted
func (c *DNSController) multiClusterServiceDeleted(key string) {
	klog.V(2).Infof("MultiClusterServiceDeleted %q deleted", key)
	c.dnsControllerMutex.Lock()
	defer c.dnsControllerMutex.Unlock()
	delete(c.remoteServiceMap, key)
}
