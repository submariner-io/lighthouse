package controller

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/submariner-io/admiral/pkg/federate"
	multiclusterservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

type LightHouseController struct {
	//remoteServices is a map that holds the remote services that are discovered.
	remoteServices map[string]*multiclusterservice.MultiClusterService

	// remoteClusters is a map of remote clusters, which have been discovered
	remoteClusters map[string]*RemoteCluster

	processingMutex *sync.Mutex

	federator federate.Federator

	// Indirection hook for unit tests to supply fake client sets
	newClientset func(kubeConfig *rest.Config) (kubernetes.Interface, error)
}

type RemoteCluster struct {
	stopCh chan struct{}

	ClusterID       string
	serviceInformer cache.SharedIndexInformer
	queue           workqueue.RateLimitingInterface
}

func New(federator federate.Federator) *LightHouseController {

	return &LightHouseController{
		federator:       federator,
		remoteServices:  make(map[string]*multiclusterservice.MultiClusterService),
		remoteClusters:  make(map[string]*RemoteCluster),
		processingMutex: &sync.Mutex{},

		newClientset: func(c *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(c)
		},
	}
}

func (c *LightHouseController) Start() error {
	err := c.federator.WatchClusters(c)
	if err != nil {
		return fmt.Errorf("Could not register cluster watch: %v", err)
	}

	return nil
}

func (c *LightHouseController) Stop() {
	c.processingMutex.Lock()
	defer c.processingMutex.Unlock()

	for _, remoteClusters := range c.remoteClusters {
		remoteClusters.close()
	}
}

func (c *LightHouseController) OnAdd(clusterID string, kubeConfig *rest.Config) {
	klog.Infof("adding cluster: %s", clusterID)

	clientSet, err := c.newClientset(kubeConfig)
	if err != nil {
		klog.Errorf("error creating clientset for cluster %s: %s", clusterID, err.Error())
		return
	}
	c.startNewServiceWatcher(clusterID, clientSet)
}

func (c *LightHouseController) OnUpdate(clusterID string, kubeConfig *rest.Config) {
	//TODO asuryana need to implement
	klog.Infof("updating cluster: %s", clusterID)
	klog.Fatalf("Not implemented yet")
}

func (c *LightHouseController) OnRemove(clusterID string) {
	c.processingMutex.Lock()
	defer c.processingMutex.Unlock()
	c.removeServiceWatcher(clusterID)
}

func (c *LightHouseController) startNewServiceWatcher(clusterID string, clientSet kubernetes.Interface) {
	serviceInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
				return clientSet.CoreV1().Services(meta_v1.NamespaceAll).List(options)
			},
			WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
				return clientSet.CoreV1().Services(meta_v1.NamespaceAll).Watch(options)
			},
		},
		&v1.Service{},
		0,
		cache.Indexers{},
	)
	remoteCluster := &RemoteCluster{
		stopCh:          make(chan struct{}),
		ClusterID:       clusterID,
		serviceInformer: serviceInformer,
		queue:           workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}
	c.processingMutex.Lock()
	c.remoteClusters[clusterID] = remoteCluster
	c.processingMutex.Unlock()
	serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				remoteCluster.queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				remoteCluster.queue.Add(key)
			}
		},
	})

	go c.Run(remoteCluster)
}

func (c *LightHouseController) removeServiceWatcher(clusterID string) {
	remoteCluster := c.remoteClusters[clusterID]
	if remoteCluster != nil {
		klog.V(2).Infof("stopping watcher for %s", clusterID)
		remoteCluster.close()
		delete(c.remoteClusters, clusterID)
	}
}

func (rc *RemoteCluster) close() {
	close(rc.stopCh)
	rc.queue.ShutDown()
	klog.Infof(" watcher queue shutdown for cluster: %s", rc.ClusterID)
}

func (c *LightHouseController) Run(remoteCluster *RemoteCluster) {
	defer utilruntime.HandleCrash()

	go remoteCluster.serviceInformer.Run(remoteCluster.stopCh)

	if !cache.WaitForCacheSync(remoteCluster.stopCh, remoteCluster.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	go c.runWorker(remoteCluster)
}

// HasSynced is required for the cache.Controller interface.
func (c *RemoteCluster) HasSynced() bool {
	return c.serviceInformer.HasSynced()
}

func (c *LightHouseController) runWorker(remoteCluster *RemoteCluster) {
	for {
		key, shutdown := remoteCluster.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for cluster \"%s\" stopped", remoteCluster.ClusterID)
			return
		}
		defer remoteCluster.queue.Done(key)

		obj, exists, err := remoteCluster.serviceInformer.GetIndexer().GetByKey(key.(string))

		if err != nil {
			klog.Errorf("Error processing %s (will retry): %v", key, err)
			// requeue the item to work on later
			remoteCluster.queue.AddRateLimited(key)
		}
		if !exists {
			//TODO Handle delete
			remoteCluster.queue.Forget(key)
		} else {

			c.ObjectCreated(obj, remoteCluster.ClusterID)
			remoteCluster.queue.Forget(key)
		}
	}
}

func (c *LightHouseController) ObjectCreated(obj interface{}, clusterID string) {
	klog.Info("ServiceHandler.ObjectCreated")
	service := obj.(*core_v1.Service)
	serviceName := service.ObjectMeta.Name
	if rs, exists := c.remoteServices[serviceName]; !exists {
		c.remoteServices[serviceName] = c.createLightHouseCRD(service, clusterID)
		klog.Infof("A new service is created and it is added to the map: %s", serviceName)
	} else {
		klog.Infof("An addService event was received for a Service already in our cache: %s, updating instead", serviceName)
		rs.Spec.Items = append(rs.Spec.Items, c.createClusterServiceInfo(service, clusterID))
	}
	err := c.federator.Distribute(c.remoteServices[serviceName])
	if err != nil {
		fmt.Println("  Distribute failed", err)
	}
}

func (c *LightHouseController) createLightHouseCRD(service *v1.Service, clusterID string) *multiclusterservice.MultiClusterService {
	multiClusterServiceInfo :=
		&[]multiclusterservice.ClusterServiceInfo{
			c.createClusterServiceInfo(service, clusterID),
		}
	clusterInfo := &multiclusterservice.MultiClusterService{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      service.ObjectMeta.Name,
			Namespace: service.ObjectMeta.Namespace,
		},
		Spec: multiclusterservice.MultiClusterServiceSpec{
			Items: *multiClusterServiceInfo,
		},
	}
	return clusterInfo
}

func (c *LightHouseController) createClusterServiceInfo(service *v1.Service, clusterID string) multiclusterservice.ClusterServiceInfo {
	return multiclusterservice.ClusterServiceInfo{
		ClusterID: clusterID,
		ServiceIP: service.Spec.ClusterIP,
	}
}

// ObjectDeleted is called when an object is deleted
func (c *LightHouseController) ObjectDeleted(obj interface{}) {
	service := obj.(*core_v1.Service)
	serviceName := service.ObjectMeta.Name
	if _, exists := c.remoteServices[serviceName]; exists {
		err := c.federator.Delete(c.remoteServices[serviceName])
		if err != nil {
			fmt.Println("  Delete failed for service.", err)
		}
		delete(c.remoteServices, serviceName)
		klog.Infof("Service is deleted from the map: %s", serviceName)
	}
	fmt.Println("ServiceHandler.ObjectDeleted")
}

// ObjectUpdated is called when an object is updated
func (c *LightHouseController) ObjectUpdated(objOld, objNew interface{}) {
	//TODO asuryana
	fmt.Println("ServiceHandler.ObjectUpdated")
}
