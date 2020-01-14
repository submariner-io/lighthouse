package controller

import (
	"fmt"
	"sync"

	"github.com/submariner-io/admiral/pkg/federate"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/apis/lighthouse.submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type multiClusterServiceMap map[string]*lighthousev1.MultiClusterService
type remoteClustersMap map[string]*remoteCluster

type LightHouseController struct {
	// multiClusterServices is a map that holds the MultiClusterService resources to distribute.
	multiClusterServices      multiClusterServiceMap
	multiClusterServicesMutex *sync.Mutex

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

const submarinerIpamGlobalIp = "submariner.io/globalIp"

func New(federator federate.Federator) *LightHouseController {
	return &LightHouseController{
		federator:                 federator,
		multiClusterServices:      make(multiClusterServiceMap),
		multiClusterServicesMutex: &sync.Mutex{},
		remoteClusters:            make(remoteClustersMap),
		remoteClustersMutex:       &sync.Mutex{},

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
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientSet.CoreV1().Services(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientSet.CoreV1().Services(metav1.NamespaceAll).Watch(options)
			},
		},
		&corev1.Service{},
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
		UpdateFunc: func(old, newObj interface{}) {
			r.handleUpdateService(old, newObj, remoteCluster)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				remoteCluster.queue.Add(key)
			}
		},
	})

	go remoteCluster.serviceInformer.Run(remoteCluster.stopCh)
	go remoteCluster.runWorker()
}

func (r *LightHouseController) removeServiceWatcher(clusterID string) {
	remoteCluster, ok := r.remoteClusters[clusterID]
	if ok {
		klog.V(2).Infof("Stopping watcher for cluster %q", clusterID)
		remoteCluster.close()
		delete(r.remoteClusters, clusterID)
	}
}

func (r *LightHouseController) handleUpdateService(old interface{}, newObj interface{}, remoteCluster *remoteCluster) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		return
	}
	oldGlobalIp := getGlobalIpFromService(old.(*corev1.Service))
	newGlobalIp := getGlobalIpFromService(newObj.(*corev1.Service))
	if oldGlobalIp != newGlobalIp {
		remoteCluster.queue.Add(key)
	}
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

func (r *remoteCluster) close() {
	close(r.stopCh)
	r.queue.ShutDown()
	klog.Infof("Watcher queue shutdown for cluster %q", r.clusterID)
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
	klog.V(6).Infof("In remoteCluster serviceCreated for cluster %q, service %#v", r.clusterID, obj)

	service := obj.(*corev1.Service)

	r.controller.multiClusterServicesMutex.Lock()
	defer r.controller.multiClusterServicesMutex.Unlock()

	mcs, exists := r.controller.multiClusterServices[key]
	if !exists {
		mcs = r.newMultiClusterService(service, r.clusterID)
		r.controller.multiClusterServices[key] = mcs
		klog.Infof("Creating a new MultiClusterService with service %q from cluster %q", key, r.clusterID)
	} else {
		isUpdate := false
		newCsi := r.newClusterServiceInfo(service, r.clusterID)
		for i, info := range mcs.Spec.Items {
			if info.ClusterID == r.clusterID {
				if info.ServiceIP == newCsi.ServiceIP {
					klog.V(2).Infof("Cluster %q already exists for MultiClusterService %q", r.clusterID, key)
					return nil
				} else {
					isUpdate = true
					mcs.Spec.Items[i] = newCsi
					klog.V(2).Infof("Updating ServiceIp for MultiClusterService %s on Cluster %s from %s to %s", key, r.clusterID, info.ServiceIP, newCsi.ServiceIP)
				}
			}
		}
		if !isUpdate {
			mcs.Spec.Items = append(mcs.Spec.Items, r.newClusterServiceInfo(service, r.clusterID))
			klog.Infof("Adding service from cluster %q to existing MultiClusterService %q", r.clusterID, key)
		}
	}

	err := r.controller.federator.Distribute(mcs)
	if err != nil {
		mcs.Spec.Items = mcs.Spec.Items[:len(mcs.Spec.Items)-1]
		return err
	}

	return nil
}

func (r *remoteCluster) newMultiClusterService(service *corev1.Service, clusterID string) *lighthousev1.MultiClusterService {
	return &lighthousev1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.ObjectMeta.Name,
			Namespace: service.ObjectMeta.Namespace,
		},
		Spec: lighthousev1.MultiClusterServiceSpec{
			Items: []lighthousev1.ClusterServiceInfo{
				r.newClusterServiceInfo(service, clusterID),
			},
		},
	}
}

func (r *remoteCluster) newClusterServiceInfo(service *corev1.Service, clusterID string) lighthousev1.ClusterServiceInfo {
	mcsIp := getGlobalIpFromService(service)
	if mcsIp == "" {
		mcsIp = service.Spec.ClusterIP
	}
	return lighthousev1.ClusterServiceInfo{
		ClusterID: clusterID,
		ServiceIP: mcsIp,
	}
}

// serviceDeleted is called when an object is deleted
func (r *remoteCluster) serviceDeleted(key string) error {
	r.controller.multiClusterServicesMutex.Lock()
	defer r.controller.multiClusterServicesMutex.Unlock()

	if mcs, exists := r.controller.multiClusterServices[key]; exists {
		var clusterServiceInfo *lighthousev1.ClusterServiceInfo
		for i, info := range mcs.Spec.Items {
			if info.ClusterID == r.clusterID {
				mcs.Spec.Items[i] = mcs.Spec.Items[len(mcs.Spec.Items)-1]
				mcs.Spec.Items = mcs.Spec.Items[:len(mcs.Spec.Items)-1]
				clusterServiceInfo = &info
				break
			}
		}

		if clusterServiceInfo == nil {
			klog.Infof("Service %q deleted from cluster %q not present in MultiClusterService", key, r.clusterID)
			return nil
		}

		if len(mcs.Spec.Items) == 0 {
			err := r.controller.federator.Delete(mcs)
			if err != nil {
				mcs.Spec.Items = append(mcs.Spec.Items, *clusterServiceInfo)
				return err
			}

			delete(r.controller.multiClusterServices, key)

			klog.Infof("Service %q has been deleted from cluster %q - deleted MultiClusterService", key, r.clusterID)
		} else {
			err := r.controller.federator.Distribute(mcs)
			if err != nil {
				mcs.Spec.Items = append(mcs.Spec.Items, *clusterServiceInfo)
				return err
			}

			klog.Infof("Service %q has been deleted from cluster %q - removed from MultiClusterService", key, r.clusterID)
		}
	}
	return nil
}
