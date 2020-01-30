package operator

import (
	"fmt"
	"os"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metaapi "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	kubeset "k8s.io/client-go/kubernetes"
	appsset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreset "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	configapiv1 "github.com/openshift/api/config/v1"
	configset "github.com/openshift/client-go/config/clientset/versioned"
	configsetv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	tunedinformers "github.com/openshift/cluster-node-tuning-operator/pkg/generated/informers/externalversions"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
)

const (
	// With the DefaultControllerRateLimiter, retries will happen at 5ms*2^(retry_n-1)
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
	// workqueue related constants
	wqKindPod             = "pod"
	wqKindNode            = "node"
	wqKindClusterOperator = "clusteroperator"
	wqKindDaemonSet       = "daemonset"
	wqKindTuned           = "tuned"
	wqKindProfile         = "profile"
)

// Controller is the controller implementation for Tuned resources
type Controller struct {
	kubeconfig *restclient.Config

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	workqueue workqueue.RateLimitingInterface

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	pc *ProfileCalculator
}

func NewController() (*Controller, error) {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		return nil, err
	}

	listers := &ntoclient.Listers{}
	clients := &ntoclient.Clients{}
	controller := &Controller{
		kubeconfig: kubeconfig,
		workqueue:  workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		listers:    listers,
		clients:    clients,
		pc:         NewProfileCalculator(listers, clients),
	}

	// Initial event to bootstrap CR if it doesn't exist.
	controller.workqueue.AddRateLimited(wqKey{kind: wqKindTuned, name: tunedv1.TunedDefaultResourceName})

	return controller, nil
}

type wqKey struct {
	kind      string // object kind
	namespace string // object namespace
	name      string // object name
	event     string // object event type (add/update/delete) or pass the full object on delete
}

// eventProcessor is a long-running function that will continually
// read and process a message on the workqueue.
func (c *Controller) eventProcessor() {
	for {
		// Wait until there is a new item in the working queue
		obj, shutdown := c.workqueue.Get()
		if shutdown {
			return
		}

		klog.V(2).Infof("got event from workqueue")
		func() {
			defer c.workqueue.Done(obj)
			var workqueueKey wqKey
			var ok bool

			if workqueueKey, ok = obj.(wqKey); !ok {
				c.workqueue.Forget(obj)
				klog.Errorf("expected wqKey in workqueue but got %#v", obj)
				return
			}

			if err := c.sync(workqueueKey); err != nil {
				// Limit retries to maxRetries.  After that, stop trying.
				if c.workqueue.NumRequeues(workqueueKey) < maxRetries {
					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.workqueue.AddRateLimited(workqueueKey)
					klog.Errorf("unable to sync(%s/%s/%s) requeued: %v", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name, err)
					return
				}
				klog.Errorf("unable to sync(%s/%s/%s) reached max retries(%d): %v", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name, maxRetries, err)
			} else {
				klog.V(1).Infof("event from workqueue (%s/%s/%s) successfully processed", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name)
			}
			// Successful processing or we're dropping an item after maxRetries unsuccessful retries
			c.workqueue.Forget(obj)
		}()
	}
}

func (c *Controller) sync(key wqKey) error {
	var (
		cr  *tunedv1.Tuned
		err error
	)
	klog.V(2).Infof("sync(): Kind %s: %s/%s", key.kind, key.namespace, key.name)

	switch {
	case key.kind == wqKindPod:
		klog.V(2).Infof("sync(): Pod %s/%s", key.namespace, key.name)

		nodeName, change, err := c.pc.podChangeHandler(key.namespace, key.name)
		if err != nil {
			if nodeName == "" {
				// Pod not scheduled (yet), ignore it
				return nil
			}
			return fmt.Errorf("failed to process Pod %s/%s change: %v", key.namespace, key.name, err)
		}
		if change {
			klog.V(2).Infof("sync(): Pod %s/%s label(s) change is Node %s wide", key.namespace, key.name, nodeName)
			// Trigger a Profile update
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.OperatorNamespace(), name: nodeName})
		}
		return nil

	case key.kind == wqKindNode:
		klog.V(2).Infof("sync(): Node: %s", key.name)

		change, err := c.pc.nodeChangeHandler(key.name)
		if err != nil {
			return fmt.Errorf("failed to process Node %s change: %v", key.name, err)
		}
		if change {
			// We need to update Profile associated with the Node
			klog.V(2).Infof("sync(): Node %s label(s) changed", key.name)
			// Trigger a Profile update
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.OperatorNamespace(), name: key.name})
		}
		return nil

	default:
	}

	if key.kind == wqKindTuned && key.name == tunedv1.TunedDefaultResourceName {
		// default Tuned changed or a bootstrap event received
		cr, err = c.syncTunedDefault()
		if err != nil {
			return fmt.Errorf("failed to sync default Tuned CR: %v", err)
		}
	} else {
		cr, err = c.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
		if err != nil {
			if errors.IsNotFound(err) {
				cr, err = c.syncTunedDefault()
				if err != nil {
					return fmt.Errorf("failed to sync default Tuned CR: %v", err)
				}
			} else {
				return fmt.Errorf("failed to get Tuned %q: %v", tunedv1.TunedDefaultResourceName, err)
			}
		}
	}
	// We have the default Tuned custom resource (cr)

	switch {
	case key.kind == wqKindDaemonSet, key.kind == wqKindClusterOperator:
		klog.V(2).Infof("sync(): DaemonSet/OperatorStatus")

		err = c.syncDaemonSet(cr)
		if err != nil {
			return fmt.Errorf("failed to sync DaemonSet: %v", err)
		}
		err = c.syncOperatorStatus()
		if err != nil {
			return fmt.Errorf("failed to sync OperatorStatus: %v", err)
		}
		return nil

	case key.kind == wqKindProfile:
		klog.V(2).Infof("sync(): Profile: %s", key.name)

		err = c.syncProfile(cr, key.name)
		if err != nil {
			return fmt.Errorf("failed to sync Profile %q: %v", key.name, err)
		}
		return nil

	default:
	}

	// Tuned CR changed
	klog.V(2).Infof("sync(): Tuned %s", tunedv1.TunedRenderedResourceName)
	err = c.syncTunedRendered(cr)
	if err != nil {
		return fmt.Errorf("failed to sync Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
	}

	klog.V(2).Infof("sync(): DaemonSet")
	err = c.syncDaemonSet(cr)
	if err != nil {
		return fmt.Errorf("failed to sync DaemonSet: %v", err)
	}

	if key.kind == wqKindTuned {
		// Tuned CR changed, this can affect all profiles, list them and trigger profile updates
		klog.V(2).Infof("sync(): Tuned: %s", key.name)

		profileList, err := c.listers.TunedProfiles.List(labels.Everything())
		if err != nil {
			return fmt.Errorf("failed to list Tuned profile %s: %v", key.name, err)
		}
		for _, profile := range profileList {
			// Trigger Profile updates
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.OperatorNamespace(), name: profile.Name})
		}
	}

	return nil
}

func (c *Controller) syncTunedDefault() (*tunedv1.Tuned, error) {
	crMf := ntomf.TunedCustomResource()

	cr, err := c.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncTunedDefault(): Tuned %q not found, creating one", tunedv1.TunedDefaultResourceName)
			cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Create(crMf)

			if err != nil {
				return cr, fmt.Errorf("failed to create Tuned %q: %v", tunedv1.TunedDefaultResourceName, err)
			}
			// Tuned resource created successfully
			return cr, nil
		}

		return nil, fmt.Errorf("failed to get Tuned %q: %v", tunedv1.TunedDefaultResourceName, err)
	} else {
		// Tuned resource found, check whether we need to update it
		if reflect.DeepEqual(crMf.Spec, cr.Spec) {
			klog.V(2).Infof("Tuned %q doesn't need updating", crMf.Name)
			return cr, nil
		}
		cr = cr.DeepCopy() // never update the objects from cache
		cr.Spec = crMf.Spec

		klog.V(2).Infof("updating Tuned %q", crMf.Name)
		cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Update(cr)
		if err != nil {
			return cr, fmt.Errorf("failed to update tuned %q: %v", crMf.Name, err)
		}
		return cr, nil
	}
}

func (c *Controller) syncTunedRendered(tuned *tunedv1.Tuned) error {
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Tuned: %v", err)
	}

	crMf := ntomf.TunedRenderedResource(tunedList)
	crMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)
	crMf.Name = tunedv1.TunedRenderedResourceName

	cr, err := c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Create(crMf)
			if err != nil {
				return fmt.Errorf("failed to create Tuned %q: %v", crMf.Name, err)
			}
			// Tuned created successfully
			return nil
		}
		return fmt.Errorf("failed to get Tuned: %v", err)
	} else {
		if reflect.DeepEqual(crMf.Spec.Profile, cr.Spec.Profile) {
			klog.V(2).Infof("Tuned %q doesn't need updating", crMf.Name)
			return nil
		}
		cr = cr.DeepCopy() // never update the objects from cache
		cr.Spec = crMf.Spec

		klog.V(2).Infof("updating Tuned %q", crMf.Name)
		_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Update(cr)
		if err != nil {
			return fmt.Errorf("failed to update tuned %q: %v", crMf.Name, err)
		}
	}

	return nil
}

func (c *Controller) syncDaemonSet(tuned *tunedv1.Tuned) error {
	dsMf := ntomf.TunedDaemonSet()
	dsMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)

	ds, err := c.listers.DaemonSets.Get(dsMf.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncDaemonSet(): DaemonSet %q not found, creating one", dsMf.Name)
			_, err = c.clients.Apps.DaemonSets(ntoconfig.OperatorNamespace()).Create(dsMf)

			if err != nil {
				return fmt.Errorf("failed to create DaemonSet: %v", err)
			}
			// DaemonSet created successfully
			return nil
		}

		return fmt.Errorf("failed to get DaemonSet %q: %v", dsMf.Name, err)
	} else {
		operatorReleaseVersion := os.Getenv("RELEASE_VERSION")
		operandReleaseVersion := ""

		for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
			if e.Name == "RELEASE_VERSION" {
				operandReleaseVersion = e.Value
				break
			}
		}

		ds = ds.DeepCopy() // never update the objects from cache
		ds.Spec = dsMf.Spec

		if operatorReleaseVersion != operandReleaseVersion {
			// Update the DaemonSet
			klog.V(2).Infof("syncDaemonSet(): operatorReleaseVersion (%s) != operandReleaseVersion (%s), updating", operatorReleaseVersion, operandReleaseVersion)
			_, err = c.clients.Apps.DaemonSets(ntoconfig.OperatorNamespace()).Update(ds)

			if err != nil {
				return fmt.Errorf("failed to update DaemonSet: %v", err)
			}
			// DaemonSet created successfully
			return nil
		}

		// DaemonSet comparison is non-trivial and expensive
		klog.V(2).Infof("syncDaemonSet(): found DaemonSet %q, not changing it", ds.Name)
	}

	return nil
}

func (c *Controller) syncProfile(tuned *tunedv1.Tuned, nodeName string) error {
	profileMf := ntomf.TunedProfile()
	profileMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)

	profileMf.Name = nodeName
	tunedProfileName, err := c.pc.calculateProfile(nodeName)
	if err != nil {
		return err
	}

	profile, err := c.listers.TunedProfiles.Get(profileMf.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("Profile %q not found, creating one", profileMf.Name)
			profileMf.Spec.Config.TunedProfile = tunedProfileName
			_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Create(profileMf)

			if err != nil {
				return fmt.Errorf("failed to create Profile: %v", err)
			}
			// DaemonSet created successfully
			return nil
		}

		return fmt.Errorf("failed to get Profile %q: %v", profileMf.Name, err)
	} else {
		if profile.Spec.Config.TunedProfile == tunedProfileName {
			klog.V(2).Infof("no need to update Profile %q", nodeName)
			return nil
		}
		profile = profile.DeepCopy() // never update the objects from cache
		profile.Spec.Config.TunedProfile = tunedProfileName

		klog.V(2).Infof("updating Profile %q", profile.Name)
		_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Update(profile)
		if err != nil {
			return fmt.Errorf("failed to update Profile: %v", err)
		}
	}

	return nil
}

func getDefaultTunedRefs(tuned *tunedv1.Tuned) []metaapi.OwnerReference {
	return []metaapi.OwnerReference{
		*metaapi.NewControllerRef(tuned, tunedv1.SchemeGroupVersion.WithKind("Tuned")),
	}
}

func (c *Controller) informerEventHandler(workqueueKey wqKey) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(o interface{}) {
			accessor, err := kmeta.Accessor(o)
			if err != nil {
				klog.Errorf("unable to get accessor for added object: %s", err)
				return
			}
			if clusterOperator, ok := o.(*configapiv1.ClusterOperator); ok {
				if clusterOperator.GetName() != tunedv1.TunedClusterOperatorResourceName {
					return
				}
			}
			klog.V(2).Infof("add event to workqueue due to %s (add)", utilObjectInfo(o))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, namespace: accessor.GetNamespace(), name: accessor.GetName()})
		},
		UpdateFunc: func(o, n interface{}) {
			newAccessor, err := kmeta.Accessor(n)
			if err != nil {
				klog.Errorf("unable to get accessor for new object: %s", err)
				return
			}
			oldAccessor, err := kmeta.Accessor(o)
			if err != nil {
				klog.Errorf("unable to get accessor for old object: %s", err)
				return
			}
			if newAccessor.GetResourceVersion() == oldAccessor.GetResourceVersion() {
				// Periodic resync will send update events for all known resources.
				// Two different versions of the same resource will always have different RVs.
				return
			}
			if clusterOperator, ok := o.(*configapiv1.ClusterOperator); ok {
				if clusterOperator.GetName() != tunedv1.TunedClusterOperatorResourceName {
					return
				}
			}

			klog.V(2).Infof("add event to workqueue due to %s (update)", utilObjectInfo(n))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, namespace: newAccessor.GetNamespace(), name: newAccessor.GetName()})
		},
		DeleteFunc: func(o interface{}) {
			object, ok := o.(metaapi.Object)
			if !ok {
				tombstone, ok := o.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("error decoding object, invalid type")
					return
				}
				object, ok = tombstone.Obj.(metaapi.Object)
				if !ok {
					klog.Errorf("error decoding object tombstone, invalid type")
					return
				}
				klog.V(4).Infof("recovered deleted object %q from tombstone", object.GetName())
			}
			if clusterOperator, ok := o.(*configapiv1.ClusterOperator); ok {
				if clusterOperator.GetName() != tunedv1.TunedClusterOperatorResourceName {
					return
				}
			}
			klog.V(2).Infof("add event to workqueue due to %s (delete)", utilObjectInfo(object))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, namespace: object.GetNamespace(), name: object.GetName()})
		},
	}
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(stopCh <-chan struct{}) error {
	defer c.workqueue.ShutDown()

	var err error

	c.clients.Kube, err = kubeset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// ClusterOperator
	c.clients.Config, err = configsetv1.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// Tuned
	c.clients.Tuned, err = tunedset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// ConfigMap and Pods (only for leader-election)
	c.clients.Core, err = coreset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// DaemonSet
	c.clients.Apps, err = appsset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// ClusterOperator
	configClient, err := configset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	// Become the leader before proceeding
	klog.Info("trying to become a leader")
	err = c.becomeLeader(ntoconfig.OperatorNamespace(), "node-tuning-operator-lock")
	if err != nil {
		klog.Fatal(err)
	}
	klog.Info("became a leader")

	// Remove any leftover ConfigMaps during upgrade from 4.[1-3] installations; drop this hack for 4.5+
	c.clients.Core.ConfigMaps(ntoconfig.OperatorNamespace()).Delete("tuned-profiles", &metaapi.DeleteOptions{})
	c.clients.Core.ConfigMaps(ntoconfig.OperatorNamespace()).Delete("tuned-recommend", &metaapi.DeleteOptions{})

	// Start the informer factories to begin populating the informer caches
	klog.Info("starting Tuned controller")

	configInformerFactory := configinformers.NewSharedInformerFactory(configClient, ntoconfig.ResyncPeriod())
	kubeNTOInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(ntoconfig.OperatorNamespace()))
	kubeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(corev1.NamespaceAll))
	tunedInformerFactory := tunedinformers.NewSharedInformerFactoryWithOptions(c.clients.Tuned, ntoconfig.ResyncPeriod(), tunedinformers.WithNamespace(ntoconfig.OperatorNamespace()))

	podInformer := kubeInformerFactory.Core().V1().Pods()
	c.listers.Pods = podInformer.Lister()
	podInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindPod}))

	nodeInformer := kubeInformerFactory.Core().V1().Nodes()
	c.listers.Nodes = nodeInformer.Lister()
	nodeInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindNode}))

	coInformer := configInformerFactory.Config().V1().ClusterOperators()
	c.listers.ClusterOperators = coInformer.Lister()
	coInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindClusterOperator}))

	dsInformer := kubeNTOInformerFactory.Apps().V1().DaemonSets()
	c.listers.DaemonSets = dsInformer.Lister().DaemonSets(ntoconfig.OperatorNamespace())
	dsInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindDaemonSet}))

	trInformer := tunedInformerFactory.Tuned().V1().Tuneds()
	c.listers.TunedResources = trInformer.Lister().Tuneds(ntoconfig.OperatorNamespace())
	trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindTuned}))

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(ntoconfig.OperatorNamespace())
	tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindProfile}))

	configInformerFactory.Start(stopCh)  // ClusterOperator
	kubeNTOInformerFactory.Start(stopCh) // DaemonSet
	kubeInformerFactory.Start(stopCh)    // Pod/Node
	tunedInformerFactory.Start(stopCh)   // Tuned/Profile

	// Wait for the caches to be synced before starting worker(s)
	klog.Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(stopCh,
		podInformer.Informer().HasSynced,
		nodeInformer.Informer().HasSynced,
		coInformer.Informer().HasSynced,
		dsInformer.Informer().HasSynced,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
	)
	if !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("starting events processor")
	go wait.Until(c.eventProcessor, time.Second, stopCh)
	klog.Info("started events processor")

	<-stopCh
	klog.Info("shutting down events processor")

	return nil
}
