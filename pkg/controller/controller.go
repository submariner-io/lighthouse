package controller

import (
	"fmt"
	"net"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/submariner-io/admiral/pkg/federate"
	multiclusterservice "github.com/submariner-io/lighthouse/pkg/apis/multiclusterservice/v1"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

// arbitrary event channel size
const eventChannelSize = 1000

type LightHouseController struct {
	//remoteServices is a map that holds the remote services that are discovered.
	remoteServices map[string]*multiclusterservice.MultiClusterService

	// remoteClusters is a map of remote clusters, which have been discovered
	remoteClusters map[string]*RemoteCluster

	// processingMutex is used to avoid synchronization issues when handling
	// objects inside the controller, it's a generalistic lock, although
	// later in time we can come up with a more granular implementation.
	processingMutex *sync.Mutex

	federator federate.Federator
}

type RemoteCluster struct {
	stopCh chan struct{}

	ClusterID       string
	ClientSet       kubernetes.Interface
	serviceInformer cache.SharedIndexInformer
	queue           workqueue.RateLimitingInterface
}

func New(federator federate.Federator) *LightHouseController {

	return &LightHouseController{
		federator:       federator,
		remoteServices:  make(map[string]*multiclusterservice.MultiClusterService),
		remoteClusters:  make(map[string]*RemoteCluster),
		processingMutex: &sync.Mutex{},
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

	clientSet, err := kubernetes.NewForConfig(kubeConfig)
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
	//TODO asuryana need to implement
	klog.Infof("removing cluster: %s", clusterID)
	klog.Fatalf("Not implemented yet")
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
		ClientSet:       clientSet,
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
	// processNextWorkItem will automatically wait until there's work available
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
	// assert the type to a Pod object to pull out relevant data
	service := obj.(*core_v1.Service)
	serviceName := service.ObjectMeta.Name
	if rs, exists := c.remoteServices[serviceName]; !exists {
		c.remoteServices[serviceName] = c.createLightHouseCRD(service, clusterID)
		klog.Infof("A new service is created and it is added to the map: %s", serviceName)
	} else {
		klog.Infof("An addService event was received for a Service already in our cache: %s, updating instead", serviceName)
		rs.Spec.Items = append(rs.Spec.Items, c.createClusterServiceInfo(service, clusterID))
	}
	//TODO asuryana need to distribute the CRD.
	//c.federator.Distribute(service)
}

func (c *LightHouseController) createLightHouseCRD(service *v1.Service, clusterID string) *multiclusterservice.MultiClusterService {
	multiClusterServiceInfo :=
		&[]multiclusterservice.ClusterServiceInfo{
			c.createClusterServiceInfo(service, clusterID),
		}
	clusterInfo := &multiclusterservice.MultiClusterService{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: service.ObjectMeta.Name,
		},
		Spec: multiclusterservice.MultiClusterServiceSpec{
			Items: *multiClusterServiceInfo,
		},
	}
	return clusterInfo
}

func (c *LightHouseController) createClusterServiceInfo(service *v1.Service, clusterID string) multiclusterservice.ClusterServiceInfo {
	return multiclusterservice.ClusterServiceInfo{
		ClusterID:     clusterID,
		ServiceIP:     net.ParseIP(service.Spec.ClusterIP),
		ClusterDomain: "example.org",
	}
}

// ObjectDeleted is called when an object is deleted
func (c *LightHouseController) ObjectDeleted(obj interface{}) {
	fmt.Println("ServiceHandler.ObjectDeleted")
}

// ObjectUpdated is called when an object is updated
func (c *LightHouseController) ObjectUpdated(objOld, objNew interface{}) {
	fmt.Println("ServiceHandler.ObjectUpdated")
}
