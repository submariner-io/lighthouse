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
	mcservice "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

type remoteServiceMap map[string]*mcservice.MultiClusterService
type remoteClustersMap map[string]*remoteCluster

type LightHouseController struct {
	// remoteServices is a map that holds the remote services that are discovered.
	remoteServices      remoteServiceMap
	remoteServicesMutex *sync.Mutex

	// remoteClusters is a map of remote clusters, which have been discovered
	remoteClusters      remoteClustersMap
	remoteClustersMutex *sync.Mutex

	federator federate.Federator

	// Indirection hook for unit tests to supply fake client sets
	newClientset func(clusterID string, kubeConfig *rest.Config) (kubernetes.Interface, error)
}

type remoteCluster struct {
	stopCh          chan struct{}
	clusterID       string
	serviceInformer cache.SharedIndexInformer
	queue           workqueue.RateLimitingInterface
	controller      *LightHouseController
}

func New(federator federate.Federator) *LightHouseController {
	return &LightHouseController{
		federator:           federator,
		remoteServices:      make(remoteServiceMap),
		remoteServicesMutex: &sync.Mutex{},
		remoteClusters:      make(remoteClustersMap),
		remoteClustersMutex: &sync.Mutex{},

		newClientset: func(clusterID string, kubeConfig *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(kubeConfig)
		},
	}
}

func (r *LightHouseController) Start() error {
	err := r.federator.WatchClusters(r)
	if err != nil {
		return fmt.Errorf("could not register cluster watch: %v", err)
	}

	return nil
}

func (r *LightHouseController) Stop() {
	r.remoteClustersMutex.Lock()
	defer r.remoteClustersMutex.Unlock()

	for _, remoteClusters := range r.remoteClusters {
		remoteClusters.close()
	}
}

func (r *LightHouseController) OnAdd(clusterID string, kubeConfig *rest.Config) {
	klog.Infof("Cluster %q added", clusterID)

	r.remoteClustersMutex.Lock()
	defer r.remoteClustersMutex.Unlock()

	r.startNewServiceWatcher(clusterID, kubeConfig)
}

func (r *LightHouseController) OnUpdate(clusterID string, kubeConfig *rest.Config) {
	klog.Infof("Cluster %q updated", clusterID)

	r.remoteClustersMutex.Lock()
	defer r.remoteClustersMutex.Unlock()

	r.removeServiceWatcher(clusterID)
	r.startNewServiceWatcher(clusterID, kubeConfig)
}

func (r *LightHouseController) OnRemove(clusterID string) {
	klog.Infof("Cluster %q removed", clusterID)

	r.remoteClustersMutex.Lock()
	defer r.remoteClustersMutex.Unlock()

	r.removeServiceWatcher(clusterID)
}

func (r *LightHouseController) startNewServiceWatcher(clusterID string, kubeConfig *rest.Config) {
	clientSet, err := r.newClientset(clusterID, kubeConfig)
	if err != nil {
		klog.Errorf("Error creating client set for cluster %q: %v", clusterID, err)
		return
	}

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

	remoteCluster := &remoteCluster{
		stopCh:          make(chan struct{}),
		clusterID:       clusterID,
		serviceInformer: serviceInformer,
		queue:           workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		controller:      r,
	}

	r.remoteClusters[clusterID] = remoteCluster

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

	go remoteCluster.run()
}

func (r *LightHouseController) removeServiceWatcher(clusterID string) {
	remoteCluster, ok := r.remoteClusters[clusterID]
	if ok {
		klog.V(2).Infof("Stopping watcher for cluster %q", clusterID)
		remoteCluster.close()
		delete(r.remoteClusters, clusterID)
	}
}

func (r *remoteCluster) close() {
	close(r.stopCh)
	r.queue.ShutDown()
	klog.Infof("Watcher queue shutdown for cluster %q", r.clusterID)
}

func (r *remoteCluster) run() {
	defer utilruntime.HandleCrash()
	go r.serviceInformer.Run(r.stopCh)
	go r.runWorker()
}

func (r *remoteCluster) runWorker() {
	for {
		keyObj, shutdown := r.queue.Get()
		if shutdown {
			klog.Infof("Lighthouse watcher for cluster %q stopped", r.clusterID)
			return
		}

		key := keyObj.(string)
		func() {
			defer r.queue.Done(key)
			obj, exists, err := r.serviceInformer.GetIndexer().GetByKey(key)

			if err != nil {
				klog.Errorf("Error retrieving service with key %q from the cache: %v", key, err)
				// requeue the item to work on later
				r.queue.AddRateLimited(key)
				return
			}

			if !exists {
				err := r.serviceDeleted(key)
				if err != nil {
					klog.Errorf("Error deleting service %q in cluster %q: %v", key, r.clusterID, err)
					r.queue.AddRateLimited(key)
					return
				}
			} else {
				err := r.serviceCreated(obj, key)
				if err != nil {
					klog.Errorf("Error creating service %q in cluster %q: %v", key, r.clusterID, err)
					r.queue.AddRateLimited(key)
					return
				}
			}

			r.queue.Forget(key)
		}()
	}
}

func (r *remoteCluster) serviceCreated(obj interface{}, key string) error {
	klog.V(2).Infof("In remoteCluster serviceCreated for cluster %q, service %#v", r.clusterID, obj)

	service := obj.(*core_v1.Service)

	r.controller.remoteServicesMutex.Lock()
	defer r.controller.remoteServicesMutex.Unlock()

	mcs, exists := r.controller.remoteServices[key]
	if !exists {
		mcs = r.createLightHouseCRD(service, r.clusterID)
		r.controller.remoteServices[key] = mcs

		klog.Infof("Creating a new MultiClusterService with service %q from cluster %q", key, r.clusterID)
	} else {
		for _, info := range mcs.Spec.Items {
			if info.ClusterID == r.clusterID {
				klog.V(2).Infof("Cluster %q already exists for MultiClusterService %q", r.clusterID, key)
				return nil
			}
		}

		mcs.Spec.Items = append(mcs.Spec.Items, r.createClusterServiceInfo(service, r.clusterID))

		klog.Infof("Adding service from cluster %q to existing MultiClusterService %q", r.clusterID, key)
	}

	err := r.controller.federator.Distribute(mcs)
	if err != nil {
		mcs.Spec.Items = mcs.Spec.Items[:len(mcs.Spec.Items)-1]
		return err
	}

	return nil
}

func (r *remoteCluster) createLightHouseCRD(service *v1.Service, clusterID string) *mcservice.MultiClusterService {
	return &mcservice.MultiClusterService{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      service.ObjectMeta.Name,
			Namespace: service.ObjectMeta.Namespace,
		},
		Spec: mcservice.MultiClusterServiceSpec{
			Items: []mcservice.ClusterServiceInfo{
				r.createClusterServiceInfo(service, clusterID),
			},
		},
	}
}

func (r *remoteCluster) createClusterServiceInfo(service *v1.Service, clusterID string) mcservice.ClusterServiceInfo {
	return mcservice.ClusterServiceInfo{
		ClusterID: clusterID,
		ServiceIP: service.Spec.ClusterIP,
	}
}

// serviceDeleted is called when an object is deleted
func (r *remoteCluster) serviceDeleted(key string) error {
	r.controller.remoteServicesMutex.Lock()
	defer r.controller.remoteServicesMutex.Unlock()

	if mcs, exists := r.controller.remoteServices[key]; exists {
		if len(mcs.Spec.Items) == 1 {
			err := r.controller.federator.Delete(mcs)
			if err != nil {
				return err
			}

			delete(r.controller.remoteServices, key)

			klog.Infof("Service %q has been deleted from cluster %q - deleted MultiClusterService", key, r.clusterID)
		} else {
			for i, service := range mcs.Spec.Items {
				if service.ClusterID == r.clusterID {
					mcs.Spec.Items[i] = mcs.Spec.Items[len(mcs.Spec.Items)-1]
					mcs.Spec.Items = mcs.Spec.Items[:len(mcs.Spec.Items)-1]
					break
				}
			}

			err := r.controller.federator.Distribute(mcs)
			if err != nil {
				return err
			}

			klog.Infof("Service %q has been deleted from cluster %q - removed from MultiClusterService", key, r.clusterID)
		}
	}
	return nil
}
