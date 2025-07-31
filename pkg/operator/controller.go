package operator

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	kubeset "k8s.io/client-go/kubernetes"
	appsset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreset "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
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

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	mcfgclientset "github.com/openshift/client-go/machineconfiguration/clientset/versioned"
	mcfginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
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
	wqKindConfigMap         = "configmap"
	wqKindMachineConfigPool = "machineconfigpool"
)

// Controller is the controller implementation for Tuned resources
type Controller struct {
	kubeconfig *restclient.Config

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	workqueue workqueue.TypedRateLimitingInterface[wqKey]

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	pod, node struct {
		informerEnabled bool
		stopCh          chan struct{}
	}

	pc *ProfileCalculator

	scheme *runtime.Scheme // used by the HyperShift code

	// bootcmdlineConflict is the internal operator's cache of Profiles
	// tracked as having kernel command-line conflict due to belonging
	// to the same MCP.
	bootcmdlineConflict map[string]bool
}

type wqKey struct {
	kind      string // object kind
	namespace string // object namespace
	name      string // object name
}

func NewController() (*Controller, error) {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		return nil, err
	}
	kubeconfig = restclient.AddUserAgent(kubeconfig, version.OperatorFilename)

	listers := &ntoclient.Listers{}
	clients := &ntoclient.Clients{}
	scheme := runtime.NewScheme()
	if err := mcfgv1.Install(scheme); err != nil {
		return nil, err
	}
	controller := &Controller{
		kubeconfig: kubeconfig,
		workqueue:  workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[wqKey]()),
		listers:    listers,
		clients:    clients,
		pc:         NewProfileCalculator(listers, clients),
		scheme:     scheme,
	}

	controller.bootcmdlineConflict = map[string]bool{}

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

	if ntoconfig.InHyperShift() {
		managementKubeconfig, err := ntoclient.GetInClusterConfig()
		if err != nil {
			return nil, err
		}
		controller.clients.ManagementKube, err = kubeset.NewForConfig(restclient.AddUserAgent(managementKubeconfig, version.OperatorFilename))
		if err != nil {
			return nil, err
		}
	}

	return controller, nil
}

// eventProcessor is a long-running function that will continually
// read and process a message on the workqueue.
func (c *Controller) eventProcessor() {
	for {
		// Wait until there is a new item in the working queue
		workqueueKey, shutdown := c.workqueue.Get()
		if shutdown {
			return
		}

		klog.V(2).Infof("got event from workqueue")
		func() {
			defer c.workqueue.Done(workqueueKey)

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
				c.workqueue.Forget(workqueueKey)
				return
			}
			klog.V(1).Infof("event from workqueue (%s/%s/%s) successfully processed", workqueueKey.kind, workqueueKey.namespace, workqueueKey.name)
			// Successful processing.
			c.workqueue.Forget(workqueueKey)
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
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.WatchNamespace(), name: nodeName})
		}
		return nil

	case key.kind == wqKindNode:
		klog.V(2).Infof("sync(): Node %s", key.name)

		change, err := c.pc.nodeChangeHandler(key.name)
		if err != nil {
			if errors.IsNotFound(err) {
				// Trigger Profile update/deletion for this node; syncProfile() will handle the deletion and also update internal data structures
				c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.WatchNamespace(), name: key.name})
				return nil
			}
			return fmt.Errorf("failed to process Node %s change: %v", key.name, err)
		}
		if change {
			// We need to update Profile associated with the Node
			klog.V(2).Infof("sync(): Node %s label(s) changed", key.name)
			// Trigger a Profile update
			c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.WatchNamespace(), name: key.name})
		}
		return nil

	case key.kind == wqKindConfigMap && key.namespace == metrics.AuthConfigMapNamespace:
		klog.V(2).Infof("sync(): wqKindConfigMap %s: %s/%s", key.kind, key.namespace, key.name)

		cm, err := c.listers.AuthConfigMapCA.Get(metrics.AuthConfigMapName)
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap %s/%s: %v", metrics.AuthConfigMapNamespace, metrics.AuthConfigMapName, err)
		}

		ca, ok := cm.Data[metrics.AuthConfigMapClientCAKey]
		if !ok {
			return fmt.Errorf("failed to find key %s in ConfigMap %s/%s", metrics.AuthConfigMapClientCAKey, metrics.AuthConfigMapNamespace, metrics.AuthConfigMapName)
		}

		metrics.DumpCA(ca)
		return nil

	case key.kind == wqKindConfigMap:
		// This should only happen in HyperShift
		klog.V(2).Infof("sync(): wqKindConfigMap %s", key.name)
		err = c.syncHostedClusterTuneds()
		if err != nil {
			return err
		}

		// If NTO-generated ConfigMap for MachineConfig is deleted,
		// we need to recreate it by syncing Profile.
		err = c.enqueueProfileUpdates()
		if err != nil {
			return err
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
		err = c.syncOperatorStatus(cr)
		if err != nil {
			return fmt.Errorf("failed to sync OperatorStatus: %v", err)
		}
		return nil

	case key.kind == wqKindProfile:
		klog.V(2).Infof("sync(): Profile %s", key.name)

		err = c.syncProfile(cr, key.name)
		if err != nil {
			return fmt.Errorf("failed to sync Profile %s: %v", key.name, err)
		}
		return nil

	default:
	}

	// Tuned CR changed and the operator components need to be managed.

	// In HyperShift clusters, any Tuned changes should be overwritten by the tuned config
	// in the management cluster
	if ntoconfig.InHyperShift() {
		err = c.syncHostedClusterTuneds()
		if err != nil {
			return fmt.Errorf("failed to sync hosted cluster Tuneds: %v", err)
		}
	}

	klog.V(2).Infof("sync(): enableInformers")
	// Enable/disable node/pod informers based on existing TuneD CRs.
	err = c.enableInformers()
	if err != nil {
		return fmt.Errorf("failed to enable/disable informers: %v", err)
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

	err = c.validateTunedCRs()
	if err != nil {
		return err
	}

	err = c.enqueueProfileUpdates()
	if err != nil {
		return err
	}

	// Tuned CR change can also mean some MachineConfigs the operator created are no longer needed;
	// removal of these will also rollback host settings such as kernel boot parameters.
	if ntoconfig.InHyperShift() {
		err = c.pruneMachineConfigsHyperShift()
		if err != nil {
			return err
		}
	} else {
		err = c.pruneMachineConfigs()
		if err != nil {
			return err
		}
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
	err = c.syncOperatorStatus(cr)
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
		c.workqueue.AddRateLimited(wqKey{kind: wqKindProfile, namespace: ntoconfig.WatchNamespace(), name: profile.Name})
	}
	return nil
}

func (c *Controller) syncTunedDefault() (*tunedv1.Tuned, error) {
	crMf := ntomf.TunedCustomResource()

	cr, err := c.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncTunedDefault(): Tuned %s not found, creating one", tunedv1.TunedDefaultResourceName)
			cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Create(context.TODO(), crMf, metav1.CreateOptions{})
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
	cr, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Update(context.TODO(), cr, metav1.UpdateOptions{})
	if err != nil {
		return cr, fmt.Errorf("failed to update Tuned %s: %v", crMf.Name, err)
	}

	return cr, nil
}

func (c *Controller) enableInformers() error {
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Tuned: %v", err)
	}

	nodeLabelsUsed := c.pc.tunedsUseNodeLabels(tunedList)
	if err = c.enableNodeInformer(nodeLabelsUsed); err != nil {
		return fmt.Errorf("failed to enable Node informer: %v", err)
	}

	// Enable/Disable Pod events based on tuned CRs using this functionality.
	// It is strongly advised not to use the Pod-label functionality in large-scale clusters.
	podLabelsUsed := c.pc.tunedsUsePodLabels(tunedList)
	if err = c.enablePodInformer(podLabelsUsed); err != nil {
		return fmt.Errorf("failed to enable Pod informer: %v", err)
	}

	return nil
}

func (c *Controller) syncDaemonSet(tuned *tunedv1.Tuned) error {
	var update bool

	dsMf := ntomf.TunedDaemonSet()
	dsMf.OwnerReferences = getDefaultTunedRefs(tuned)

	ds, err := c.listers.DaemonSets.Get(dsMf.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncDaemonSet(): DaemonSet %s not found, creating one", dsMf.Name)
			_, err = c.clients.Apps.DaemonSets(ntoconfig.WatchNamespace()).Create(context.TODO(), dsMf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create DaemonSet: %v", err)
			}
			// DaemonSet created successfully
			return nil
		}

		return fmt.Errorf("failed to get DaemonSet %s: %v", dsMf.Name, err)
	}

	operatorReleaseVersion := os.Getenv("RELEASE_VERSION")
	operandReleaseVersion := c.getDaemonSetReleaseVersion(ds)

	if operatorReleaseVersion != operandReleaseVersion {
		klog.V(2).Infof("syncDaemonSet(): operatorReleaseVersion (%s) != operandReleaseVersion (%s), updating", operatorReleaseVersion, operandReleaseVersion)
		update = true
	}

	// OCPBUGS-18480: sync the DaemonSet also when the operand image changes
	operandImageCurrent := ds.Spec.Template.Spec.Containers[0].Image
	operandImageWanted := os.Getenv("CLUSTER_NODE_TUNED_IMAGE")
	if operandImageCurrent != operandImageWanted {
		klog.V(2).Infof("syncDaemonSet(): operandImageCurrent (%s) != operandImageWanted (%s), updating", operandImageCurrent, operandImageWanted)
		update = true
	}

	if update {
		// Update the DaemonSet
		ds = ds.DeepCopy() // never update the objects from cache
		ds.Spec = dsMf.Spec

		_, err = c.clients.Apps.DaemonSets(ntoconfig.WatchNamespace()).Update(context.TODO(), ds, metav1.UpdateOptions{})
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
	profileMf.OwnerReferences = getDefaultTunedRefs(tuned)

	profileMf.Name = nodeName
	delete(c.bootcmdlineConflict, nodeName)
	nodeLabels, err := c.pc.nodeLabelsGet(nodeName)
	if err != nil {
		// Remove Profiles for Nodes which no longer exist.
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncProfile(): deleting Profile %s", nodeName)
			err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.WatchNamespace()).Delete(context.TODO(), nodeName, metav1.DeleteOptions{})
			if err != nil && errors.IsNotFound(err) {
				err = nil
			}
		}
		return err
	}
	if nodeLabels["kubernetes.io/os"] != "linux" {
		klog.Infof("ignoring non-linux Node %s", nodeName)
		return nil
	}

	// Check if node labels are already cached.  If not, do not sync.
	// Profile update/sync is triggered later on once node labels are cached on
	// the node event.
	if c.pc.state.nodeLabels[nodeName] == nil {
		return nil
	}

	var computed ComputedProfile
	if ntoconfig.InHyperShift() {
		computed, err = c.pc.calculateProfileHyperShift(nodeName)
	} else {
		computed, err = c.pc.calculateProfile(nodeName)
	}
	switch err.(type) {
	case nil:
	case *DuplicateProfileError:
		// Stop.  We have TuneD profiles with the same name and different contents.
		// Do not spam the logs with this error, it will be reported during Tuned CR
		// updates and periodic resync/validation.
		return nil
	default:
		return err
	}

	metrics.ProfileCalculated(profileMf.Name, computed.TunedProfileName)

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

			klog.V(2).Infof("syncProfile(): Profile %s not found, creating one [%s]", profileMf.Name, computed.TunedProfileName)
			profileMf.Annotations = updateDeferredAnnotation(profileMf.Annotations, computed.Deferred)
			profileMf.Spec.Config.TunedProfile = computed.TunedProfileName
			profileMf.Spec.Config.Debug = computed.Operand.Debug
			profileMf.Spec.Config.Verbosity = computed.Operand.Verbosity
			profileMf.Spec.Config.TuneDConfig = computed.Operand.TuneDConfig
			profileMf.Spec.Profile = computed.AllProfiles
			profileMf.Status.Conditions = tunedpkg.InitializeStatusConditions()
			_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.WatchNamespace()).Create(context.TODO(), profileMf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create Profile %s: %v", profileMf.Name, err)
			}
			// Profile created successfully
			klog.Infof("created profile %s [%s]", profileMf.Name, computed.TunedProfileName)
			return nil
		}

		return fmt.Errorf("failed to get Profile %s: %v", profileMf.Name, err)
	}

	// Profiles carry status conditions based on which OperatorStatus is also
	// calculated.
	err = c.syncOperatorStatus(tuned)
	if err != nil {
		return fmt.Errorf("failed to sync OperatorStatus: %v", err)
	}

	providerName, err := c.getProviderName(nodeName)
	if err != nil {
		return fmt.Errorf("failed to get ProviderName: %v", err)
	}

	if ntoconfig.InHyperShift() {
		// nodePoolName is the name of the NodePool which the Node corresponding to this Profile
		// is a part of. If nodePoolName is the empty string, it either means that Node label
		// based matching was used, or we don't know the NodePool, so we should not sync the
		// MachineConfigs.
		if computed.NodePoolName != "" {
			if profile.Status.TunedProfile == computed.TunedProfileName && profileApplied(profile) {
				// Synchronize MachineConfig only once the (calculated) TuneD profile 'tunedProfileName'
				// has been successfully applied.
				err := c.syncMachineConfigHyperShift(computed.NodePoolName, profile)
				if err != nil {
					return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
				}
			}
		}
	} else {
		if computed.MCLabels != nil {
			// The TuneD daemon profile 'tunedProfileName' for nodeName matched with MachineConfig
			// labels 'mcLabels' set for additional machine configuration.  Sync the operator-created
			// MachineConfig based on 'mcLabels'.
			if profile.Status.TunedProfile == computed.TunedProfileName && profileApplied(profile) {
				// Synchronize MachineConfig only once the (calculated) TuneD profile 'tunedProfileName'
				// has been successfully applied.
				err := c.syncMachineConfig(computed.MCLabels, profile)
				if err != nil {
					return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
				}
			}
		}
	}

	anns := updateDeferredAnnotation(profile.Annotations, computed.Deferred)

	// Minimize updates
	if profile.Spec.Config.TunedProfile == computed.TunedProfileName &&
		profile.Spec.Config.Debug == computed.Operand.Debug &&
		profile.Spec.Config.Verbosity == computed.Operand.Verbosity &&
		reflect.DeepEqual(profile.Spec.Config.TuneDConfig, computed.Operand.TuneDConfig) &&
		reflect.DeepEqual(profile.Spec.Profile, computed.AllProfiles) &&
		util.GetDeferredUpdateAnnotation(profile.Annotations) == util.GetDeferredUpdateAnnotation(anns) &&
		profile.Spec.Config.ProviderName == providerName {
		klog.V(2).Infof("syncProfile(): no need to update Profile %s", nodeName)
		return nil
	}
	profile = profile.DeepCopy() // never update the objects from cache
	profile.Annotations = anns
	profile.Spec.Config.TunedProfile = computed.TunedProfileName
	profile.Spec.Config.Debug = computed.Operand.Debug
	profile.Spec.Config.Verbosity = computed.Operand.Verbosity
	profile.Spec.Config.TuneDConfig = computed.Operand.TuneDConfig
	profile.Spec.Config.ProviderName = providerName
	profile.Spec.Profile = computed.AllProfiles
	profile.Status.Conditions = tunedpkg.InitializeStatusConditions()
	delete(c.pc.state.bootcmdline, nodeName) // bootcmdline retrieved from node annotation is potentially stale, let it resync on node update

	klog.V(2).Infof("syncProfile(): updating Profile %s [%s]", profile.Name, computed.TunedProfileName)
	_, err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.WatchNamespace()).Update(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
	}
	klog.Infof("updated profile %s [%s] (deferred=%v)", profile.Name, computed.TunedProfileName, util.GetDeferredUpdateAnnotation(profile.Annotations))

	return nil
}

func updateDeferredAnnotation(anns map[string]string, mode util.DeferMode) map[string]string {
	if util.IsDeferredUpdate(mode) {
		return util.SetDeferredUpdateAnnotation(anns, mode)
	}
	return util.DeleteDeferredUpdateAnnotation(anns)
}

func (c *Controller) getProviderName(nodeName string) (string, error) {
	node, err := c.listers.Nodes.Get(nodeName)
	if err != nil {
		return "", err
	}

	return util.GetProviderName(node.Spec.ProviderID), nil
}

func (c *Controller) syncMachineConfig(labels map[string]string, profile *tunedv1.Profile) error {
	var (
		bootcmdline     string
		kernelArguments []string
	)

	pools, err := c.pc.getPoolsForMachineConfigLabelsSorted(labels)
	if err != nil {
		return err
	}

	// The following enforces per-pool machineConfigLabels selectors.
	if len(pools) > 1 {
		// Log an error and do not requeue, this is a configuration issue.
		klog.Errorf("profile %v uses machineConfigLabels that match across multiple MCPs (%v); this is not supported",
			profile.Name, printMachineConfigPoolsNames(pools))
		return nil
	}

	name := GetMachineConfigNameForPools(pools)
	klog.V(2).Infof("syncMachineConfig(): %v", name)

	nodes, err := c.pc.getNodesForPool(pools[0])
	if err != nil {
		return err
	}

	if ok := c.allNodesHaveBootcmdlineSet(nodes); !ok {
		klog.V(2).Infof("syncMachineConfig(): bootcmdline for %s not cached for all nodes, sync canceled", profile.Name)
		return nil
	}

	bootcmdline = c.pc.state.bootcmdline[profile.Name]
	if ok := c.allNodesAgreeOnBootcmdline(nodes); !ok {
		// Log an error and do not requeue, this is a configuration issue.
		klog.Errorf("not all %d Nodes in MCP %v agree on bootcmdline: %s", len(nodes), pools[0].Name, bootcmdline)
		return nil
	}

	kernelArguments = util.SplitKernelArguments(bootcmdline)

	annotations := map[string]string{GeneratedByControllerVersionAnnotationKey: version.Version}

	mc, err := c.listers.MachineConfigs.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncMachineConfig(): MachineConfig %s not found, creating one", name)
			if len(bootcmdline) == 0 {
				// Creating a new MachineConfig with empty kernelArguments only causes unnecessary node
				// reboots.
				klog.V(2).Infof("not creating a MachineConfig with empty kernelArguments")
				return nil
			}
			mc = NewMachineConfig(name, annotations, labels, kernelArguments)
			_, err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Create(context.TODO(), mc, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create MachineConfig %s: %v", mc.Name, err)
			}
			klog.Infof("created MachineConfig %s with%s", mc.Name, MachineConfigGenerationLogLine(len(bootcmdline) != 0, bootcmdline))
			return nil
		}
		return err
	}

	mcNew := NewMachineConfig(name, annotations, labels, kernelArguments)

	kernelArgsEq := util.StringSlicesEqual(mc.Spec.KernelArguments, kernelArguments)
	if kernelArgsEq {
		// No update needed
		klog.V(2).Infof("syncMachineConfig(): MachineConfig %s doesn't need updating", mc.Name)
		return nil
	}
	mc = mc.DeepCopy() // never update the objects from cache
	mc.Annotations = mcNew.Annotations
	mc.Spec.KernelArguments = kernelArguments
	mc.Spec.Config = mcNew.Spec.Config

	l := MachineConfigGenerationLogLine(!kernelArgsEq, bootcmdline)
	klog.V(2).Infof("syncMachineConfig(): updating MachineConfig %s with%s", mc.Name, l)
	_, err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Update(context.TODO(), mc, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update MachineConfig %s: %v", mc.Name, err)
	}

	klog.Infof("updated MachineConfig %s with%s", mc.Name, l)

	return nil
}

// allNodesHaveBootcmdlineSet returns true if all Nodes in slice 'nodes' have
// their bootcmdline annotation (TunedBootcmdlineAnnotationKey) value cached in
// the profilecalculator's cache.  The values in the cache are populated on Node
// updates (regular ones or just resyncs) and removed from the cache on Profile
// updates.  Note this is not a bullet-proof solution to false bootcmdline
// conflicts, because we can still have races, such as regular k8s object
// Node resync (or other independent Node updates) right after Profile update.
// While the cached bootcmdline value will be deleted after Profile update, it
// can still be populated by a stale value from Node's annotation because of the
// Node's resync (or other independent updates) before the proper bootcmdline
// is calculated by the TuneD pod and Node's (TunedBootcmdlineAnnotationKey)
// annotation updated.  A bullet-proof solution would involve adding a new
// annotation such as tuned.openshift.io/lastProfileObservedGen to the Node
// tied to the Profile and comparing it to the Profile's generation prior to
// updating a MachineConfig.  However, we want to avoid putting extra load
// on the API server as much as possible and this simpler solution already
// significantly reduces false reports of bootcmdline conflicts.
func (c *Controller) allNodesHaveBootcmdlineSet(nodes []*corev1.Node) bool {
	for _, node := range nodes {
		if v, bootcmdlineSet := c.pc.state.bootcmdline[node.Name]; !bootcmdlineSet {
			klog.V(3).Infof("allNodesHaveBootcmdlineSet(): bootcmdline not set for node %s", node.Name)
			return false
		} else {
			klog.V(3).Infof("allNodesHaveBootcmdlineSet(): bootcmdline %q set for node %s", v, node.Name)
		}
	}

	return true
}

// allNodesAgreeOnBootcmdline returns true if the current cached annotation 'TunedBootcmdlineAnnotationKey'
// of all Nodes in slice 'nodes' has the same value.
func (c *Controller) allNodesAgreeOnBootcmdline(nodes []*corev1.Node) bool {
	if len(nodes) == 0 {
		return true
	}

	match := true
	bootcmdline := c.pc.state.bootcmdline[nodes[0].Name]
	for _, node := range nodes[1:] {
		if bootcmdline != c.pc.state.bootcmdline[node.Name] {
			klog.V(2).Infof("found a conflicting bootcmdline %q for Node %q", c.pc.state.bootcmdline[node.Name], node.Name)
			c.bootcmdlineConflict[node.Name] = true
			match = false
		}
	}

	return match
}

func (c *Controller) syncMachineConfigHyperShift(nodePoolName string, profile *tunedv1.Profile) error {
	var (
		kernelArguments []string
	)

	mcName := MachineConfigPrefix + "-" + nodePoolName
	configMapName := mcConfigMapName(nodePoolName)

	nodes, err := c.getNodesForNodePool(nodePoolName)
	if err != nil {
		return fmt.Errorf("could not fetch a list of Nodes for NodePool %s: %v", nodePoolName, err)
	}

	if ok := c.allNodesHaveBootcmdlineSet(nodes); !ok {
		klog.V(2).Infof("syncMachineConfigHyperShift(): bootcmdline for %s not cached for all nodes, sync canceled", profile.Name)
		return nil
	}

	bootcmdline := c.pc.state.bootcmdline[profile.Name]
	if ok := c.allNodesAgreeOnBootcmdline(nodes); !ok {
		return fmt.Errorf("not all %d Nodes in NodePool %v agree on bootcmdline: %s", len(nodes), nodePoolName, bootcmdline)
	}

	kernelArguments = util.SplitKernelArguments(bootcmdline)

	annotations := map[string]string{GeneratedByControllerVersionAnnotationKey: version.Version}

	mcConfigMap, err := c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncMachineConfigHyperShift(): ConfigMap %s not found, creating one", configMapName)
			if len(bootcmdline) == 0 {
				// Creating a new MachineConfig with empty kernelArguments/Ignition only causes unnecessary node
				// reboots.
				klog.V(2).Infof("not creating a MachineConfig with empty kernelArguments")
				return nil
			}
			mc := NewMachineConfig(mcName, annotations, nil, kernelArguments)

			// put the MC into a ConfigMap and create that instead
			mcConfigMap, err = c.newConfigMapForMachineConfig(configMapName, nodePoolName, mc)
			if err != nil {
				klog.Errorf("failed to generate ConfigMap %s for MachineConfig %s: %v", configMapName, mc.Name, err)
				return nil
			}
			_, err = c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).Create(context.TODO(), mcConfigMap, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create ConfigMap %s for MachineConfig %s: %v", configMapName, mc.Name, err)
			}
			klog.Infof("created ConfigMap %s for MachineConfig %s with%s", configMapName, mc.Name, MachineConfigGenerationLogLine(len(bootcmdline) != 0, bootcmdline))
			return nil
		}
		return err
	}

	// A ConfigMap with the same name was found
	// we need to make sure the contents are up-to-date.
	mc, err := c.getMachineConfigFromConfigMap(mcConfigMap)
	if err != nil {
		klog.Errorf("failed to get MachineConfig from ConfigMap %s: %v", mcConfigMap.Name, err)
		return nil
	}

	mcNew := NewMachineConfig(mcName, annotations, nil, kernelArguments)

	// Compare kargs between existing and new mcfg
	kernelArgsEq := util.StringSlicesEqual(mc.Spec.KernelArguments, kernelArguments)

	// Check ConfigMap labels and annotations
	neededLabels := generatedConfigMapLabels(nodePoolName)
	cmLabels := mcConfigMap.GetLabels()
	neededAnnotations := generatedConfigMapAnnotations(nodePoolName)
	cmAnnotations := mcConfigMap.GetAnnotations()
	cmLabelsAndAnnotationsCorrect := util.MapOfStringsContains(cmLabels, neededLabels) && util.MapOfStringsContains(cmAnnotations, neededAnnotations)

	// If mcfgs are equivalent don't update
	if kernelArgsEq && cmLabelsAndAnnotationsCorrect {
		// No update needed
		klog.V(2).Infof("syncMachineConfigHyperShift(): MachineConfig %s doesn't need updating", mc.Name)
		return nil
	}

	// If mcfgs are not equivalent do update
	mc = mc.DeepCopy() // never update the objects from cache
	mc.Annotations = mcNew.Annotations
	mc.Spec.KernelArguments = kernelArguments
	mc.Spec.Config = mcNew.Spec.Config

	l := MachineConfigGenerationLogLine(!kernelArgsEq, bootcmdline)
	klog.V(2).Infof("syncMachineConfigHyperShift(): updating MachineConfig %s with%s", mc.Name, l)

	newData, err := c.serializeMachineConfig(mc)
	if err != nil {
		klog.Errorf("failed to serialize ConfigMap for MachineConfig %s: %v", mc.Name, err)
		return nil
	}
	mcConfigMap.Data[mcConfigMapDataKey] = string(newData)
	for k, v := range neededLabels {
		mcConfigMap.Labels[k] = v
	}
	for k, v := range neededAnnotations {
		mcConfigMap.Annotations[k] = v
	}

	_, err = c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).Update(context.TODO(), mcConfigMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap for MachineConfig %s: %v", mcConfigMap.Name, err)
	}

	klog.Infof("updated ConfigMap %s for MachineConfig %s with%s", mcConfigMap.Name, mc.Name, l)

	return nil
}

// pruneMachineConfigs removes any MachineConfigs created by the operator that are not selected by any of the TuneD daemon profile.
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
		if mc.Annotations != nil {
			if _, ok := mc.Annotations[GeneratedByControllerVersionAnnotationKey]; !ok {
				continue
			}
			// mc's annotations have the controller/operator key

			if mcNames[mc.Name] {
				continue
			}
			// This MachineConfig has this operator's annotations and it is not currently used by any
			// Tuned CR; remove it and let MCO roll-back any changes

			klog.V(2).Infof("pruneMachineConfigs(): deleting MachineConfig %s", mc.Name)
			err = c.clients.MC.MachineconfigurationV1().MachineConfigs().Delete(context.TODO(), mc.Name, metav1.DeleteOptions{})
			if err != nil {
				// Unable to delete the MachineConfig
				return err
			}
			klog.Infof("deleted MachineConfig %s", mc.Name)
		}
	}

	return nil
}

// pruneMachineConfigs removes any MachineConfigs created by the operator that are not selected by any of the TuneD daemon profile.
func (c *Controller) pruneMachineConfigsHyperShift() error {
	cmListOptions := metav1.ListOptions{
		LabelSelector: operatorGeneratedMachineConfig + "=true",
	}
	cmList, err := c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).List(context.TODO(), cmListOptions)
	if err != nil {
		return err
	}

	mcNames, err := c.getConfigMapNamesForTuned()
	if err != nil {
		return err
	}

	for _, cm := range cmList.Items {
		if cm.Annotations != nil {
			if _, ok := cm.Annotations[GeneratedByControllerVersionAnnotationKey]; !ok {
				continue
			}
			// mc's annotations have the controller/operator key
			if mcNames[cm.Name] {
				continue
			}

			// This ConfigMap has this operator's annotations and it is not currently used by any
			// Tuned CR; remove it and let MCO roll-back any changes
			klog.V(2).Infof("pruneMachineConfigsHyperShift(): deleting ConfigMap %s", cm.Name)
			err = c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).Delete(context.TODO(), cm.Name, metav1.DeleteOptions{})
			if err != nil {
				// Unable to delete the ConfigMap
				return err
			}
			klog.Infof("deleted MachineConfig ConfigMap %s", cm.Name)
		}
	}

	return nil
}

// Get daemonset release version.
func (c *Controller) getDaemonSetReleaseVersion(ds *appsv1.DaemonSet) string {
	var operandReleaseVersion string

	for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "RELEASE_VERSION" {
			operandReleaseVersion = e.Value
			break
		}
	}

	return operandReleaseVersion
}

// Get all operator MachineConfig names for all TuneD daemon profiles.
func (c *Controller) getMachineConfigNamesForTuned() (map[string]bool, error) {
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list Tuned: %v", err)
	}

	mcNames := map[string]bool{}

	for _, recommend := range TunedRecommend(tunedList) {
		if recommend.Profile == nil || recommend.MachineConfigLabels == nil {
			continue
		}

		pools, err := c.pc.getPoolsForMachineConfigLabels(recommend.MachineConfigLabels)
		if err != nil {
			return nil, err
		}
		mcName := GetMachineConfigNameForPools(pools)

		mcNames[mcName] = true
	}

	return mcNames, nil
}

// Get all operator ConfigMap names for all TuneD daemon profiles.
func (c *Controller) getConfigMapNamesForTuned() (map[string]bool, error) {
	// In HyperShift, we only consider the default profile and
	// the Tuned profiles from Tuneds referenced in this Nodes NodePool spec.
	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list Tuneds: %v", err)
	}

	cmNames := map[string]bool{}
	for _, tuned := range tunedList {
		nodePoolName := tuned.Labels[hypershiftNodePoolNameLabel]
		for _, recommend := range tuned.Spec.Recommend {
			// nodePoolName may be an empty string in the case of the default profile
			if recommend.Profile == nil || recommend.Match != nil || nodePoolName == "" {
				continue
			}

			// recommend.Profile not nil, recommend.Match is nil, and we have nodePoolName
			cmName := mcConfigMapName(nodePoolName)
			cmNames[cmName] = true
		}
	}

	return cmNames, nil
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
			if clusterOperator, ok := o.(*configapiv1.ClusterOperator); ok {
				if clusterOperator.GetName() != tunedv1.TunedClusterOperatorResourceName {
					// Don't add ClusterOperator updates for ClusterOperator objects we do not own.
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
		if _, err := informer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindNode})); err != nil {
			return err
		}

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
		if _, err := informer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindPod})); err != nil {
			return err
		}

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

// Remove this function and associated code in the future.  This is for cleanup during upgrades only.
// The rendered resource is no longer used.
func (c *Controller) removeTunedRendered() error {
	var err error

	_, err = c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			// Do not create any noise when TunedRenderedResourceName is not found (was already removed).
			err = nil
		} else {
			err = fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		}
	} else {
		err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Delete(context.TODO(), tunedv1.TunedRenderedResourceName, metav1.DeleteOptions{})
		if err != nil {
			err = fmt.Errorf("failed to delete Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		} else {
			klog.Infof("deleted Tuned %s", tunedv1.TunedRenderedResourceName)
		}
	}
	return err
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
		err = c.clients.Apps.DaemonSets(ntoconfig.WatchNamespace()).Delete(ctx, dsMf.Name, metav1.DeleteOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to delete DaemonSet %s: %v", dsMf.Name, err)
		} else {
			klog.Infof("deleted DaemonSet %s", dsMf.Name)
		}
	}

	// Remove this code in the future.  This is for cleanup during upgrades only.
	// The rendered resource is no longer used.
	if err := c.removeTunedRendered(); err != nil {
		lastErr = err
	}

	profileList, err := c.listers.TunedProfiles.List(labels.Everything())
	if err != nil {
		lastErr = fmt.Errorf("failed to list Tuned Profiles: %v", err)
	}
	for _, profile := range profileList {
		err = c.clients.Tuned.TunedV1().Profiles(ntoconfig.WatchNamespace()).Delete(ctx, profile.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to delete Profile %s: %v", profile.Name, err)
		} else {
			klog.Infof("deleted Profile %s", profile.Name)
		}
	}

	if !ntoconfig.InHyperShift() {
		err = c.pruneMachineConfigs()
		if err != nil {
			lastErr = fmt.Errorf("failed to prune operator-created MachineConfigs: %v", err)
		}
	}

	return lastErr
}

// run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) run(ctx context.Context) error {
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("starting Tuned controller")

	configInformerFactory := configinformers.NewSharedInformerFactory(c.clients.ConfigClientSet, ntoconfig.ResyncPeriod())
	kubeNTOInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube, ntoconfig.ResyncPeriod(), kubeinformers.WithNamespace(ntoconfig.WatchNamespace()))
	tunedInformerFactory := tunedinformers.NewSharedInformerFactoryWithOptions(c.clients.Tuned, ntoconfig.ResyncPeriod(), tunedinformers.WithNamespace(ntoconfig.WatchNamespace()))

	coInformer := configInformerFactory.Config().V1().ClusterOperators()
	c.listers.ClusterOperators = coInformer.Lister()
	if _, err := coInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindClusterOperator})); err != nil {
		return err
	}

	dsInformer := kubeNTOInformerFactory.Apps().V1().DaemonSets()
	c.listers.DaemonSets = dsInformer.Lister().DaemonSets(ntoconfig.WatchNamespace())
	if _, err := dsInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindDaemonSet})); err != nil {
		return err
	}

	trInformer := tunedInformerFactory.Tuned().V1().Tuneds()
	c.listers.TunedResources = trInformer.Lister().Tuneds(ntoconfig.WatchNamespace())
	if _, err := trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindTuned})); err != nil {
		return err
	}

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(ntoconfig.WatchNamespace())
	if _, err := tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindProfile})); err != nil {
		return err
	}

	InformerFuncs := []cache.InformerSynced{
		coInformer.Informer().HasSynced,
		dsInformer.Informer().HasSynced,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
	}

	var tunedConfigMapInformerFactory kubeinformers.SharedInformerFactory
	var mcfgConfigMapInformerFactory kubeinformers.SharedInformerFactory
	var mcfgInformerFactory mcfginformers.SharedInformerFactory
	var caConfigMapInformerFactory kubeinformers.SharedInformerFactory
	if ntoconfig.InHyperShift() {
		tunedConfigMapInformerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.ManagementKube,
			ntoconfig.ResyncPeriod(),
			kubeinformers.WithNamespace(ntoconfig.OperatorNamespace()),
			kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.LabelSelector = tunedConfigMapLabel + "=true"
			}))
		tunedConfigMapInformer := tunedConfigMapInformerFactory.Core().V1().ConfigMaps()
		c.listers.ConfigMaps = tunedConfigMapInformer.Lister().ConfigMaps(ntoconfig.OperatorNamespace())
		if _, err := tunedConfigMapInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindConfigMap})); err != nil {
			return err
		}

		mcfgConfigMapInformerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.ManagementKube,
			ntoconfig.ResyncPeriod(),
			kubeinformers.WithNamespace(ntoconfig.OperatorNamespace()),
			kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.LabelSelector = operatorGeneratedMachineConfig + "=true"
			}))
		mcfgConfigMapInformer := mcfgConfigMapInformerFactory.Core().V1().ConfigMaps()
		c.listers.ConfigMaps = mcfgConfigMapInformer.Lister().ConfigMaps(ntoconfig.OperatorNamespace())
		if _, err := mcfgConfigMapInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindConfigMap})); err != nil {
			return err
		}

		InformerFuncs = append(InformerFuncs, tunedConfigMapInformer.Informer().HasSynced, mcfgConfigMapInformer.Informer().HasSynced)
	} else {
		mcfgInformerFactory = mcfginformers.NewSharedInformerFactory(c.clients.MC, ntoconfig.ResyncPeriod())
		mcInformer := mcfgInformerFactory.Machineconfiguration().V1().MachineConfigs()

		c.listers.MachineConfigs = mcInformer.Lister()

		mcpInformer := mcfgInformerFactory.Machineconfiguration().V1().MachineConfigPools()
		c.listers.MachineConfigPools = mcpInformer.Lister()
		if _, err := mcpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindMachineConfigPool})); err != nil {
			return err
		}
		InformerFuncs = append(InformerFuncs, mcInformer.Informer().HasSynced, mcpInformer.Informer().HasSynced)

		caConfigMapInformerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.clients.Kube,
			ntoconfig.ResyncPeriod(),
			kubeinformers.WithNamespace(metrics.AuthConfigMapNamespace),
			kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = "metadata.name=" + "extension-apiserver-authentication"
			}))
		caInformer := caConfigMapInformerFactory.Core().V1().ConfigMaps()
		c.listers.AuthConfigMapCA = caInformer.Lister().ConfigMaps(metrics.AuthConfigMapNamespace)
		if _, err := caInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindConfigMap, namespace: metrics.AuthConfigMapNamespace})); err != nil {
			return err
		}
		InformerFuncs = append(InformerFuncs, caInformer.Informer().HasSynced)
	}

	configInformerFactory.Start(ctx.Done())  // ClusterOperator
	kubeNTOInformerFactory.Start(ctx.Done()) // DaemonSet
	tunedInformerFactory.Start(ctx.Done())   // Tuned/Profile

	if ntoconfig.InHyperShift() {
		tunedConfigMapInformerFactory.Start(ctx.Done())
		mcfgConfigMapInformerFactory.Start(ctx.Done())
	} else {
		mcfgInformerFactory.Start(ctx.Done())        // MachineConfig/MachineConfigPool
		caConfigMapInformerFactory.Start(ctx.Done()) // Metrics client's ConfigMap CA
	}

	// Wait for the caches to be synced before starting worker(s)
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(ctx.Done(), InformerFuncs...)
	if !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Remove this code in the future.  This is for cleanup during upgrades only.
	// The rendered resource is no longer used.
	if err := c.removeTunedRendered(); err != nil {
		klog.Error(err)
	}

	klog.V(1).Info("starting events processor")
	go wait.Until(c.eventProcessor, time.Second, ctx.Done())
	klog.Info("started events processor/controller")

	<-ctx.Done()
	if err := c.enableNodeInformer(false); err != nil {
		klog.Errorf("failed to disable Node informer: %v", err)
	}
	if err := c.enablePodInformer(false); err != nil {
		klog.Errorf("failed to disable Pod informer: %v", err)
	}
	klog.Info("shutting down events processor/controller")
	return nil
}

func (c *Controller) Start(ctx context.Context) error {
	return c.run(ctx)
}

func (c *Controller) NeedLeaderElection() bool {
	return true
}

func tunedMapFromList(tuneds []tunedv1.Tuned) map[string]tunedv1.Tuned {
	ret := map[string]tunedv1.Tuned{}
	for _, tuned := range tuneds {
		ret[tuned.Name] = tuned
	}
	return ret
}
