package operator

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	kubeset "k8s.io/client-go/kubernetes"
	appsset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreset "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configapiv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclientset "github.com/openshift/client-go/config/clientset/versioned"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	tunedinformers "github.com/openshift/cluster-node-tuning-operator/pkg/generated/informers/externalversions"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	tunedpkg "github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"

	ign3types "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/google/uuid"
	mcfgclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned"
	mcfginformers "github.com/openshift/machine-config-operator/pkg/generated/informers/externalversions"
)

const (
	// With the DefaultControllerRateLimiter, retries will happen at 5ms*2^(retry_n-1)
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
	// workqueue related constants
	wqKindPod               = "pod"
	wqKindNode              = "node"
	wqKindClusterOperator   = "clusteroperator"
	wqKindDaemonSet         = "daemonset"
	wqKindTuned             = "tuned"
	wqKindProfile           = "profile"
	wqKindMachineConfigPool = "machineconfigpool"
)

// Controller is the controller implementation for Tuned resources
type Controller struct {
	kubeconfig *restclient.Config

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	workqueue workqueue.RateLimitingInterface

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	pod, node struct {
		informerEnabled bool
		stopCh          chan struct{}
	}

	pc *ProfileCalculator
}

type wqKey struct {
	kind      string // object kind
	namespace string // object namespace
	name      string // object name
	event     string // object event type (add/update/delete) or pass the full object on delete
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

	controller.clients.Kube, err = kubeset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// ClusterOperator
	controller.clients.ConfigV1Client, err = configv1client.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// Tuned
	controller.clients.Tuned, err = tunedset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// MachineConfig
	controller.clients.MC, err = mcfgclientset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// ConfigMap and Pods (only for leader-election)
	controller.clients.Core, err = coreset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// DaemonSet
	controller.clients.Apps, err = appsset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	// ClusterOperator
	controller.clients.ConfigClientSet, err = configclientset.NewForConfig(controller.kubeconfig)
	if err != nil {
		return nil, err
	}

	return controller, nil
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
				requeued := c.workqueue.NumRequeues(workqueueKey)
				// Limit retries to maxRetries.  After that, stop trying.
				if requeued < maxRetries {
					klog.Errorf("unable to sync(%s/%s/%s) requeued (%d): %v", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name, requeued, err)

					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.workqueue.AddRateLimited(workqueueKey)
					return
				}
				klog.Errorf("unable to sync(%s/%s/%s) reached max retries (%d): %v", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name, maxRetries, err)
				// Dropping the item after maxRetries unsuccessful retries.
				c.workqueue.Forget(obj)
				return
			}
			klog.V(1).Infof("event from workqueue (%s/%s/%s) successfully processed", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name)
			// Successful processing.
			c.workqueue.Forget(obj)
		}()
	}
}

func (c *Controller) sync(key wqKey) error {
	var (
		cr           *tunedv1.Tuned
		err, lastErr error
	)
	klog.V(2).Infof("sync(): Kind %s: %s/%s", key.kind, key.namespace, key.name)

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
				return fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
			}
		}
	}
	// We have the default Tuned custom resource (cr)

	switch cr.Spec.ManagementState {
	case operatorv1.Force:
		// Use the same logic as Managed.
	case operatorv1.Managed, "":
		// Managed means that the operator is actively managing its resources and trying to keep the component active.
	case operatorv1.Removed:
		// Removed means that the operator is actively managing its resources and trying to remove all traces of the component.
		lastErr = c.removeResources()
		goto out
	case operatorv1.Unmanaged:
		// Unmanaged means that the operator will not take any action related to the component.
		goto out
	default:
		// This should never happen due to openAPIV3Schema checks.
		klog.Warningf("unknown custom resource ManagementState: %s", cr.Spec.ManagementState)
	}
	// Operator is in Force or Managed state.

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
		klog.V(2).Infof("sync(): Node %s", key.name)

		change, err := c.pc.nodeChangeHandler(key.name)
		if err != nil {
			if errors.IsNotFound(err) {
				// Do not leave any leftover profiles after node deletions
				klog.V(2).Infof("sync(): deleting Profile %s", key.name)
				err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Delete(context.TODO(), key.name, metav1.DeleteOptions{})
				if err != nil && !errors.IsNotFound(err) {
					return fmt.Errorf("failed to delete Profile %s: %v", key.name, err)
				}
				klog.Infof("deleted Profile %s", key.name)
				return nil
			}
			return fmt.Errorf("failed to process Node %s change: %v", key.name, err)
		}
		if change {
			// We need to update Profile associated with the Node
			klog.V(2).Infof("sync(): Node %s label(s) changed", key.name)
			// Trigger a Profile update
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.OperatorNamespace(), name: key.name})
		}
		return nil

	case key.kind == wqKindMachineConfigPool:
		klog.V(2).Infof("sync(): MachineConfigPool %s", key.name)

		// MachineConfigPool changes may mean the operator-created MachineConfigs are no longer needed.
		// Note this is not only direct MachineConfigPool changes, but also indirect changes, such as
		// removing labels from nodes so that they no longer are part of a given MCP.
		err = c.pruneMachineConfigs()
		if err != nil {
			return err
		}

		// MachineConfigPool changes can affect all nodes and MCP is where cluster admins
		// will adjust the operator behavior when using the MachineConfig functionality.
		// Nodes can become part of the pool or they can lose the pool membership.
		// Trigger profile calculations/updates for all Tuned Profiles in the cluster.
		err = c.enqueueProfileUpdates()
		if err != nil {
			return err
		}

		return nil

	case key.kind == wqKindDaemonSet, key.kind == wqKindClusterOperator:
		klog.V(2).Infof("sync(): DaemonSet/OperatorStatus")

		err = c.syncDaemonSet(cr)
		if err != nil {
			return fmt.Errorf("failed to sync DaemonSet: %v", err)
		}
		err = c.syncOperatorStatus(cr, nil)
		if err != nil {
			return fmt.Errorf("failed to sync OperatorStatus: %v", err)
		}
		return nil

	case key.kind == wqKindProfile:
		klog.V(2).Infof("sync(): Profile %s", key.name)

		// Check if node labels are already cached.  If not, ignore this event.
		// Profile update is triggered later on once node labels are cached on
		// the node event.
		if c.pc.state.nodeLabels[key.name] == nil {
			return nil
		}

		err = c.syncProfile(cr, key.name)
		if err != nil {
			return fmt.Errorf("failed to sync Profile %s: %v", key.name, err)
		}
		return nil

	default:
	}

	// Tuned CR changed and the operator components need to be managed.
	klog.V(2).Infof("sync(): Tuned %s", tunedv1.TunedRenderedResourceName)
	err = c.syncTunedRendered(cr)
	if err != nil {
		return fmt.Errorf("failed to sync Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
	}

	if key.name != tunedv1.TunedDefaultResourceName {
		crTuned, err := c.listers.TunedResources.Get(key.name)
		if err != nil {
			if !errors.IsNotFound(err) {
				return err
			}
		} else if crTuned.Spec.ManagementState != "" {
			klog.Warningf("setting ManagementState is supported only in Tuned/%s; ignoring ManagementState in Tuned/%s", tunedv1.TunedDefaultResourceName, key.name)
		}
	}

	klog.V(2).Infof("sync(): DaemonSet")
	err = c.syncDaemonSet(cr)
	if err != nil {
		return fmt.Errorf("failed to sync DaemonSet: %v", err)
	}

	// Tuned CR changed, this can affect all profiles, list them and trigger profile updates
	klog.V(2).Infof("sync(): Tuned %s", key.name)

	err = c.enqueueProfileUpdates()
	if err != nil {
		return err
	}

	if key.name == tunedv1.TunedRenderedResourceName {
		// Do not start unused MachineConfig pruning unnecessarily for the rendered resource
		return nil
	}

	// Tuned CR change can also mean some MachineConfigs the operator created are no longer needed;
	// removal of these will also rollback host settings such as kernel boot parameters.
	err = c.pruneMachineConfigs()
	if err != nil {
		return err
	}

	return nil

out:
	err = c.enableNodeInformer(false)
	if err != nil {
		lastErr = fmt.Errorf("failed to disable Node informer: %v", err)
	}
	err = c.enablePodInformer(false)
	if err != nil {
		lastErr = fmt.Errorf("failed to disable Pod informer: %v", err)
	}
	err = c.syncOperatorStatus(cr, nil)
	if err != nil {
		lastErr = fmt.Errorf("failed to synchronize Operator status: %v", err)
	}
	return lastErr
}

// enqueueProfileUpdates enqueues profile calculations/updates for all Tuned Profiles in the cluster.
func (c *Controller) enqueueProfileUpdates() error {
	profileList, err := c.listers.TunedProfiles.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Tuned Profiles: %v", err)
	}
	for _, profile := range profileList {
		// Enqueue Profile updates into the operator's workqueue
		c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.OperatorNamespace(), name: profile.Name})
	}
	return nil
}

func (c *Controller) syncTunedDefault() (*tunedv1.Tuned, error) {
	crMf := ntomf.TunedCustomResource()

	cr, err := c.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncTunedDefault(): Tuned %s not found, creating one", tunedv1.TunedDefaultResourceName)
			cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Create(context.TODO(), crMf, metav1.CreateOptions{})
			if err != nil {
				return cr, fmt.Errorf("failed to create Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
			}
			// Tuned resource created successfully
			return cr, nil
		}

		return nil, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
	}

	// Tuned resource found, check whether we need to update it
	if reflect.DeepEqual(crMf.Spec.Profile, cr.Spec.Profile) &&
		reflect.DeepEqual(crMf.Spec.Recommend, cr.Spec.Recommend) {
		klog.V(2).Infof("syncTunedDefault(): Tuned %s doesn't need updating", crMf.Name)
		return cr, nil
	}
	cr = cr.DeepCopy() // never update the objects from cache
	cr.Spec.Profile = crMf.Spec.Profile
	cr.Spec.Recommend = crMf.Spec.Recommend

	klog.V(2).Infof("syncTunedDefault(): updating Tuned %s", crMf.Name)
	cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Update(context.TODO(), cr, metav1.UpdateOptions{})
	if err != nil {
		return cr, fmt.Errorf("failed to update Tuned %s: %v", crMf.Name, err)
	}
	return cr, nil
}

func (c *Controller) syncTunedRendered(tuned *tunedv1.Tuned) error {
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Tuned: %v", err)
	}

	crMf := ntomf.TunedRenderedResource(tunedList)
	crMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)
	crMf.Name = tunedv1.TunedRenderedResourceName

	nodeLabelsUsed := c.pc.tunedsUseNodeLabels(tunedList)
	c.enableNodeInformer(nodeLabelsUsed)

	// Enable/Disable Pod events based on tuned CRs using this functionality.
	// It is strongly advised not to use the Pod-label functionality in large-scale clusters.
	podLabelsUsed := c.pc.tunedsUsePodLabels(tunedList)
	c.enablePodInformer(podLabelsUsed)

	cr, err := c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncTunedRendered(): Tuned %s not found, creating one", crMf.Name)
			_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Create(context.TODO(), crMf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create Tuned %s: %v", crMf.Name, err)
			}
			// Tuned created successfully
			klog.Infof("created Tuned %s", crMf.Name)
			return nil
		}
		return fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
	}

	if reflect.DeepEqual(crMf.Spec.Profile, cr.Spec.Profile) {
		klog.V(2).Infof("syncTunedRendered(): Tuned %s doesn't need updating", crMf.Name)
		return nil
	}
	cr = cr.DeepCopy() // never update the objects from cache
	cr.Spec = crMf.Spec

	klog.V(2).Infof("syncTunedRendered(): updating Tuned %s", cr.Name)
	_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Update(context.TODO(), cr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Tuned %s: %v", cr.Name, err)
	}
	klog.Infof("updated Tuned %s", cr.Name)

	return nil
}

func (c *Controller) syncDaemonSet(tuned *tunedv1.Tuned) error {
	dsMf := ntomf.TunedDaemonSet()
	dsMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)

	ds, err := c.listers.DaemonSets.Get(dsMf.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncDaemonSet(): DaemonSet %s not found, creating one", dsMf.Name)
			_, err = c.clients.Apps.DaemonSets(ntoconfig.OperatorNamespace()).Create(context.TODO(), dsMf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create DaemonSet: %v", err)
			}
			// DaemonSet created successfully
			return nil
		}

		return fmt.Errorf("failed to get DaemonSet %s: %v", dsMf.Name, err)
	}

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
		_, err = c.clients.Apps.DaemonSets(ntoconfig.OperatorNamespace()).Update(context.TODO(), ds, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update DaemonSet: %v", err)
		}
		// DaemonSet created successfully
		return nil
	}

	// DaemonSet comparison is non-trivial and expensive
	klog.V(2).Infof("syncDaemonSet(): found DaemonSet %s [%s], not changing it", ds.Name, operatorReleaseVersion)

	return nil
}

func (c *Controller) syncProfile(tuned *tunedv1.Tuned, nodeName string) error {
	profileMf := ntomf.TunedProfile()
	profileMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(tuned)

	profileMf.Name = nodeName
	tunedProfileName, mcLabels, pools, daemonDebug, err := c.pc.calculateProfile(nodeName)
	if err != nil {
		return err
	}
	metrics.ProfileCalculated(profileMf.Name, tunedProfileName)

	profile, err := c.listers.TunedProfiles.Get(profileMf.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = c.listers.Nodes.Get(nodeName)
			if err != nil {
				if errors.IsNotFound(err) {
					// Node not found, do not create a profile for a node that does not exist
					return nil
				}
				return err
			}

			klog.V(2).Infof("syncProfile(): Profile %s not found, creating one [%s]", profileMf.Name, tunedProfileName)
			profileMf.Spec.Config.TunedProfile = tunedProfileName
			profileMf.Spec.Config.Debug = daemonDebug
			profileMf.Status.Conditions = tunedpkg.InitializeStatusConditions()
			_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Create(context.TODO(), profileMf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create Profile %s: %v", profileMf.Name, err)
			}
			// Profile created successfully
			klog.Infof("created profile %s [%s]", profileMf.Name, tunedProfileName)
			return nil
		}

		return fmt.Errorf("failed to get Profile %s: %v", profileMf.Name, err)
	}

	// Profiles carry status conditions based on which OperatorStatus is also
	// calculated.
	err = c.syncOperatorStatus(tuned, profile)
	if err != nil {
		return fmt.Errorf("failed to sync OperatorStatus: %v", err)
	}

	if mcLabels != nil {
		// The Tuned daemon profile 'tunedProfileName' for nodeName matched with MachineConfig
		// labels set for additional machine configuration.  Sync the operator-created
		// MachineConfig for MachineConfigPools 'pools'.
		if profile.Status.TunedProfile == tunedProfileName && profileApplied(profile) {
			// Synchronize MachineConfig only once the (calculated) TuneD profile 'tunedProfileName'
			// has been successfully applied.
			err := c.syncMachineConfig(getMachineConfigNameForPools(pools), mcLabels, profile.Status.Bootcmdline, profile.Status.Stalld)
			if err != nil {
				return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
			}
		}
	}
	if profile.Spec.Config.TunedProfile == tunedProfileName &&
		profile.Spec.Config.Debug == daemonDebug {
		klog.V(2).Infof("syncProfile(): no need to update Profile %s", nodeName)
		return nil
	}
	profile = profile.DeepCopy() // never update the objects from cache
	profile.Spec.Config.TunedProfile = tunedProfileName
	profile.Spec.Config.Debug = daemonDebug
	profile.Status.Conditions = tunedpkg.InitializeStatusConditions()

	klog.V(2).Infof("syncProfile(): updating Profile %s [%s]", profile.Name, tunedProfileName)
	_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Update(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
	}
	klog.Infof("updated profile %s [%s]", profile.Name, tunedProfileName)

	return nil
}

func (c *Controller) syncMachineConfig(name string, labels map[string]string, bootcmdline string, stalld *bool) error {
	var (
		kernelArguments []string
		ignFiles        []ign3types.File
		ignUnits        []ign3types.Unit
	)
	logline := func(bIgn, bCmdline bool, bootcmdline string) string {
		var (
			sb strings.Builder
		)

		if bIgn {
			sb.WriteString(" ignition")
			if bCmdline {
				sb.WriteString(" and")
			}
		}

		if bCmdline {
			sb.WriteString(" kernel parameters: [")
			sb.WriteString(bootcmdline)
			sb.WriteString("]")
		}

		return sb.String()
	}
	kernelArguments = util.SplitKernelArguments(bootcmdline)
	ignFiles = tunedpkg.ProvideIgnitionFiles(stalld)
	ignUnits = tunedpkg.ProvideSystemdUnits(stalld)

	annotations := map[string]string{GeneratedByControllerVersionAnnotationKey: version.Version}

	mc, err := c.listers.MachineConfigs.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncMachineConfig(): MachineConfig %s not found, creating one", name)
			haveIgnition := len(ignFiles) != 0 || len(ignUnits) != 0
			if len(bootcmdline) == 0 && !haveIgnition {
				// Creating a new MachineConfig with empty kernelArguments/Ignition only causes unnecessary node
				// reboots.
				klog.V(2).Infof("not creating a MachineConfig with empty kernelArguments/Ignition")
				return nil
			}
			mc = newMachineConfig(name, annotations, labels, kernelArguments, ignFiles, ignUnits)
			_, err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Create(context.TODO(), mc, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create MachineConfig %s: %v", mc.ObjectMeta.Name, err)
			}
			klog.Infof("created MachineConfig %s with%s", mc.ObjectMeta.Name, logline(haveIgnition, len(bootcmdline) != 0, bootcmdline))
			return nil
		}
		return err
	}

	mcNew := newMachineConfig(name, annotations, labels, kernelArguments, ignFiles, ignUnits)

	ignEq, err := ignEqual(mc, mcNew)
	if err != nil {
		return fmt.Errorf("failed to sync MachineConfig %s: %v", mc.ObjectMeta.Name, err)
	}
	kernelArgsEq := util.StringSlicesEqual(mc.Spec.KernelArguments, kernelArguments)
	if kernelArgsEq && ignEq {
		// No update needed
		klog.V(2).Infof("syncMachineConfig(): MachineConfig %s doesn't need updating", mc.ObjectMeta.Name)
		return nil
	}
	mc = mc.DeepCopy() // never update the objects from cache
	mc.Spec.KernelArguments = kernelArguments
	mc.Spec.Config = mcNew.Spec.Config

	l := logline(!ignEq, !kernelArgsEq, bootcmdline)
	klog.V(2).Infof("syncMachineConfig(): updating MachineConfig %s with%s", mc.ObjectMeta.Name, l)
	_, err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Update(context.TODO(), mc, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update MachineConfig %s: %v", mc.ObjectMeta.Name, err)
	}

	klog.Infof("updated MachineConfig %s with%s", mc.ObjectMeta.Name, l)

	return nil
}

// pruneMachineConfigs removes any MachineConfigs created by the operator that are not selected by any of the Tuned daemon profile.
func (c *Controller) pruneMachineConfigs() error {
	mcList, err := c.listers.MachineConfigs.List(labels.Everything())
	if err != nil {
		return err
	}

	mcNames, err := c.getMachineConfigNamesForTuned()
	if err != nil {
		return err
	}

	for _, mc := range mcList {
		if mc.ObjectMeta.Annotations != nil {
			if _, ok := mc.ObjectMeta.Annotations[GeneratedByControllerVersionAnnotationKey]; !ok {
				continue
			}
			// mc's annotations have the controller/operator key

			if mcNames[mc.ObjectMeta.Name] {
				continue
			}
			// This MachineConfig has this operator's annotations and it is not currently used by any
			// Tuned CR; remove it and let MCO roll-back any changes

			klog.V(2).Infof("pruneMachineConfigs(): deleting MachineConfig %s", mc.ObjectMeta.Name)
			err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Delete(context.TODO(), mc.ObjectMeta.Name, metav1.DeleteOptions{})
			if err != nil {
				// Unable to delete the MachineConfig
				return err
			}
			klog.Infof("deleted MachineConfig %s", mc.ObjectMeta.Name)
		}
	}

	return nil
}

// Get all operator MachineConfig names for all Tuned daemon profiles.
func (c *Controller) getMachineConfigNamesForTuned() (map[string]bool, error) {
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list Tuned: %v", err)
	}

	mcNames := map[string]bool{}

	for _, recommend := range tunedRecommend(tunedList) {
		if recommend.Profile == nil || recommend.MachineConfigLabels == nil {
			continue
		}

		pools, err := c.pc.getPoolsForMachineConfigLabels(recommend.MachineConfigLabels)
		if err != nil {
			return nil, err
		}
		mcName := getMachineConfigNameForPools(pools)

		mcNames[mcName] = true
	}

	return mcNames, nil
}

func getDefaultTunedRefs(tuned *tunedv1.Tuned) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		*metav1.NewControllerRef(tuned, tunedv1.SchemeGroupVersion.WithKind("Tuned")),
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
			klog.V(2).Infof("add event to workqueue due to %s (add)", util.ObjectInfo(o))
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
			klog.V(2).Infof("add event to workqueue due to %s (update)", util.ObjectInfo(n))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, namespace: newAccessor.GetNamespace(), name: newAccessor.GetName()})
		},
		DeleteFunc: func(o interface{}) {
			object, ok := o.(metav1.Object)
			if !ok {
				tombstone, ok := o.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("error decoding object, invalid type")
					return
				}
				object, ok = tombstone.Obj.(metav1.Object)
				if !ok {
					klog.Errorf("error decoding object tombstone, invalid type")
					return
				}
				klog.V(4).Infof("recovered deleted object %s from tombstone", object.GetName())
			}
			if clusterOperator, ok := o.(*configapiv1.ClusterOperator); ok {
				if clusterOperator.GetName() != tunedv1.TunedClusterOperatorResourceName {
					return
				}
			}
			klog.V(2).Infof("add event to workqueue due to %s (delete)", util.ObjectInfo(object))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, namespace: object.GetNamespace(), name: object.GetName()})
		},
	}
}

// enableNodeInformer enables/disables event handling for Nodes.
func (c *Controller) enableNodeInformer(enable bool) error {
	if (enable && c.node.informerEnabled) || (!enable && !c.node.informerEnabled) {
		return nil
	}

	if enable {
		var (
			informerFactory kubeinformers.SharedInformerFactory
			informer        corev1informers.NodeInformer
		)
		c.node.stopCh = make(chan struct{})
		informerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(corev1.NamespaceAll))

		informer = informerFactory.Core().V1().Nodes()
		c.listers.Nodes = informer.Lister()
		informer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindNode}))

		informerFactory.Start(c.node.stopCh)
	} else {
		defer close(c.node.stopCh)
		c.node.stopCh <- struct{}{}
		c.pc.nodeLabelsDelete()
	}

	c.node.informerEnabled = enable
	return nil
}

// enablePodInformer enables/disables event handling for Pods.
func (c *Controller) enablePodInformer(enable bool) error {
	if (enable && c.pod.informerEnabled) || (!enable && !c.pod.informerEnabled) {
		return nil
	}

	if enable {
		var (
			informerFactory kubeinformers.SharedInformerFactory
			informer        corev1informers.PodInformer
		)
		c.pod.stopCh = make(chan struct{})
		informerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(corev1.NamespaceAll))

		informer = informerFactory.Core().V1().Pods()
		c.listers.Pods = informer.Lister()
		informer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindPod}))

		informerFactory.Start(c.pod.stopCh)
	} else {
		defer close(c.pod.stopCh)
		c.pod.stopCh <- struct{}{}
		c.pc.podLabelsDelete()
	}

	c.pod.informerEnabled = enable
	metrics.PodLabelsUsed(enable)
	return nil
}

func (c *Controller) removeResources() error {
	var lastErr error
	dsMf := ntomf.TunedDaemonSet()
	ctx := context.TODO()

	_, err := c.listers.DaemonSets.Get(dsMf.Name)
	if err != nil {
		if !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to get DaemonSet %s: %v", dsMf.Name, err)
		}
	} else {
		err = c.clients.Apps.DaemonSets(ntoconfig.OperatorNamespace()).Delete(ctx, dsMf.Name, metav1.DeleteOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to delete DaemonSet %s: %v", dsMf.Name, err)
		} else {
			klog.Infof("deleted DaemonSet %s", dsMf.Name)
		}
	}

	_, err = c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
	if err != nil {
		if !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		}
	} else {
		err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Delete(ctx, tunedv1.TunedRenderedResourceName, metav1.DeleteOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to delete Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		} else {
			klog.Infof("deleted Tuned %s", tunedv1.TunedRenderedResourceName)
		}
	}

	profileList, err := c.listers.TunedProfiles.List(labels.Everything())
	if err != nil {
		lastErr = fmt.Errorf("failed to list Tuned Profiles: %v", err)
	}
	for _, profile := range profileList {
		err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.OperatorNamespace()).Delete(ctx, profile.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to delete Profile %s: %v", profile.Name, err)
		} else {
			klog.Infof("deleted Profile %s", profile.Name)
		}
	}

	err = c.pruneMachineConfigs()
	if err != nil {
		lastErr = fmt.Errorf("failed to prune operator-created MachineConfigs: %v", err)
	}

	return lastErr
}

// run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) run(stopCh <-chan struct{}) {
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("starting Tuned controller")

	configInformerFactory := configinformers.NewSharedInformerFactory(c.clients.ConfigClientSet, ntoconfig.ResyncPeriod())
	kubeNTOInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(ntoconfig.OperatorNamespace()))
	tunedInformerFactory := tunedinformers.NewSharedInformerFactoryWithOptions(c.clients.Tuned, ntoconfig.ResyncPeriod(), tunedinformers.WithNamespace(ntoconfig.OperatorNamespace()))
	mcfgInformerFactory := mcfginformers.NewSharedInformerFactory(c.clients.MC, ntoconfig.ResyncPeriod())

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

	mcInformer := mcfgInformerFactory.Machineconfiguration().V1().MachineConfigs()
	c.listers.MachineConfigs = mcInformer.Lister()

	mcpInformer := mcfgInformerFactory.Machineconfiguration().V1().MachineConfigPools()
	c.listers.MachineConfigPools = mcpInformer.Lister()
	mcpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindMachineConfigPool}))

	configInformerFactory.Start(stopCh)  // ClusterOperator
	kubeNTOInformerFactory.Start(stopCh) // DaemonSet
	tunedInformerFactory.Start(stopCh)   // Tuned/Profile
	mcfgInformerFactory.Start(stopCh)    // MachineConfig/MachineConfigPool

	// Wait for the caches to be synced before starting worker(s)
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(stopCh,
		coInformer.Informer().HasSynced,
		dsInformer.Informer().HasSynced,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
		mcInformer.Informer().HasSynced,
		mcpInformer.Informer().HasSynced,
	)
	if !ok {
		klog.Error("failed to wait for caches to sync")
		return
	}

	klog.V(1).Info("starting events processor")
	go wait.Until(c.eventProcessor, time.Second, stopCh)
	klog.Info("started events processor/controller")

	<-stopCh
	c.enableNodeInformer(false)
	c.enablePodInformer(false)
	klog.Info("shutting down events processor/controller")
}

// BecomeLeader uses client-go leader-with-lease election mechanism to
// select a leader and starts the controller by calling operator run
// method.
func (c *Controller) BecomeLeader(stopCh <-chan struct{}) {
	const (
		// values below taken from:
		// https://github.com/openshift/enhancements/pull/832/files#diff-2e28754e69aa417e5b6d89e99e42f05bfb6330800fa823753383db1d170fbc2fR183
		// see rhbz#1986477 for more detail
		leaseDuration = 137 * time.Second
		renewDeadline = 107 * time.Second
		retryPeriod   = 26 * time.Second
	)

	// Become the leader before proceeding.
	klog.Info("trying to become a leader")

	// Check the presence of the legacy node-tuning-operator-lock ConfigMap.
	// If the owner references exist in the ConfigMap, we have an old garbage
	// collection based locking scheme and we cannot continue until the ConfigMap
	// is GC or deleted manually.  If the owner references do not exist, just go
	// ahead with the new client-go leader-election code.
loop:
	for {
		gcLockFound, err := c.usingGCLocking(ntoconfig.OperatorNamespace(), ntoconfig.OperatorLockName)

		switch {
		case err != nil:
			klog.Errorf("failed retrieve operator lock %s/%s: %v",
				ntoconfig.OperatorNamespace(), ntoconfig.OperatorLockName, err)
		case gcLockFound:
			klog.Errorf("garbage collector locking scheme found, waiting for operator lock %s/%s removal",
				ntoconfig.OperatorNamespace(), ntoconfig.OperatorLockName)
		default:
			break loop
		}

		select {
		case <-time.After(5 * time.Second):
			continue
		}
	}

	// Use a Go context so we can tell the leaderelection code when we
	// want to step down.
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Generate leader election ID.
	id, err := os.Hostname()
	if err != nil {
		klog.Errorf("os.Hostname() failed, using only UUID for the lock identity: %v", err)
	}
	id = id + "_" + uuid.New().String()

	lock := &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Name:      ntoconfig.OperatorLockName,
			Namespace: ntoconfig.OperatorNamespace(),
		},
		Client: c.clients.Core,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	// Start the leader election code loop.
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   leaseDuration,
		RenewDeadline:   renewDeadline,
		RetryPeriod:     retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				klog.Infof("became leader: %s", id)
				c.run(stopCh)
				runCancel()
			},
			OnStoppedLeading: func() {
				klog.Infof("leader lost")
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == id {
					// we became the leader
					return
				}
				klog.Infof("current leader: %s", identity)
			},
		},
	})
}
