package operator

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	ign3types "github.com/coreos/ignition/v2/config/v3_2/types"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorv1 "github.com/openshift/api/operator/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	tunedpkg "github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

type tunedState struct {
	podEventsEnabled bool
}

// TunedReconciler reconciles a Tuned object.
type TunedReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	state    tunedState
}

// SetupWithManager creates a new Tuned Controller and adds it to the Manager.
// The Manager will set fields on the Controller and start it when the Manager is started.
func (r *TunedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	profilePredicates := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !r.validateUpdateEvent(&e) {
				return false
			}

			profileOld := e.ObjectOld.(*tunedv1.Profile)
			profileNew := e.ObjectNew.(*tunedv1.Profile)

			return !reflect.DeepEqual(profileOld.Status.Conditions, profileNew.Status.Conditions)
		},
	}

	podPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return r.state.podEventsEnabled
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return r.state.podEventsEnabled
		},
	}

	mcpPredicates := predicate.Or(
		predicate.GenerationChangedPredicate{},
		predicate.LabelChangedPredicate{},
		predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}
			mcpOld := e.ObjectOld.(*mcov1.MachineConfigPool)
			mcpNew := e.ObjectNew.(*mcov1.MachineConfigPool)
			return !reflect.DeepEqual(mcpOld.Spec, mcpNew.Spec)
		}})

	// Field indexer is required for listing pods matching the spec.nodeName field.
	// The alternative is to use an uncached client.
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
		pod := o.(*corev1.Pod)
		return []string{pod.Spec.NodeName}
	}); err != nil {
		return err
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&tunedv1.Tuned{}).
		Owns(&tunedv1.Profile{}, builder.WithPredicates(profilePredicates)).
		Watches(
			&source.Kind{Type: &corev1.Node{}},
			handler.EnqueueRequestsFromMapFunc(r.defaultTunedRequests),
			//TODO - consider using builder.OnlyMetadata instead these perdicats
			builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			handler.EnqueueRequestsFromMapFunc(r.podLabelsToTuned),
			builder.WithPredicates(podPredicates,
				predicate.And(
					predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
						return r.state.podEventsEnabled
					}}, predicate.LabelChangedPredicate{}))).
		Watches(
			&source.Kind{Type: &mcov1.MachineConfigPool{}},
			handler.EnqueueRequestsFromMapFunc(r.defaultTunedRequests),
			builder.WithPredicates(mcpPredicates)).
		Complete(r)
	if err != nil {
		return err
	}
	r.state.podEventsEnabled = false
	return nil
}

func (r *TunedReconciler) defaultTunedRequests(mcpObj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	defaultTuned, err := r.getDefaultTuned(context.TODO())
	if err != nil {
		return nil
	}
	requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: defaultTuned.Namespace,
		Name:      defaultTuned.Name,
	}})
	return requests
}

func (r *TunedReconciler) podLabelsToTuned(podObj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	if r.state.podEventsEnabled == false {
		return nil
	}

	defaultTuned, err := r.getDefaultTuned(context.TODO())
	if err != nil {
		return nil
	}
	requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: defaultTuned.Namespace,
		Name:      defaultTuned.Name,
	}})
	return requests
}

// Reconcile reconciles when a Tuned object is created, updated and deleted.
// It makes changes based on the state read and what is in the Tuned.Spec.
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *TunedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).Infof("reconciling Tuned")

	//TODO - consider using  custom req.NamespacedName for special events such as node deletion.
	// see if this is not an abuse.
	// Fetch the Tuned instance
	tunedInstance := &tunedv1.Tuned{}
	err := r.Get(ctx, req.NamespacedName, tunedInstance)
	if err != nil {
		// For deletion events perform all the needed syncs as a regular event.
		if !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	}
	err = r.reconcileResource(ctx, tunedInstance)

	if err != nil {
		klog.Errorf("failed to reconcile Tuned %q: %v", tunedInstance.Name, err)
		r.Recorder.Eventf(tunedInstance, corev1.EventTypeWarning, "Creation failed", "Failed to create all components: %v", err)
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *TunedReconciler) reconcileResource(ctx context.Context, tuned *tunedv1.Tuned) error {
	klog.V(2).Infof("reconcileResource(): Kind %s: %s/%s", tuned.Kind, tuned.Namespace, tuned.Name)
	defaultTuned, err := r.getDefaultTuned(ctx)

	if err != nil {
		return fmt.Errorf("failed to get default Tuned CR: %v", err)
	}

	switch defaultTuned.Spec.ManagementState {
	case operatorv1.Force:
		// Use the same logic as Managed.
	case operatorv1.Managed, "":
		// Managed means that the operator is actively managing its resources and trying to keep the component active.
	case operatorv1.Removed:
		// Removed means that the operator is actively managing its resources and trying to remove all traces of the component.
		if err := r.removeResources(ctx); err != nil {
			return fmt.Errorf("failed to remove resources on Removed operator state: %v", err)
		}
		return nil
	case operatorv1.Unmanaged:
		// Unmanaged means that the operator will not take any action related to the component.
		return nil
	default:
		// This should never happen due to openAPIV3Schema checks.
		klog.Warningf("unknown custom resource ManagementState: %s", defaultTuned.Spec.ManagementState)
	}
	// Operator is in Force or Managed state.

	klog.V(2).Infof("sync(): Tuned %s", tunedv1.TunedRenderedResourceName)
	err = r.syncTunedRendered(ctx, defaultTuned)
	if err != nil {
		return fmt.Errorf("failed to sync Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
	}

	if tuned.Name != tunedv1.TunedDefaultResourceName {
		if tuned.Spec.ManagementState != "" {
			klog.Warningf("setting ManagementState is supported only in Tuned/%s; ignoring ManagementState in Tuned/%s", tunedv1.TunedDefaultResourceName, tuned.Name)
		}
	}

	// Tuned CR changed, this can affect all profiles, list them and trigger profile updates.
	klog.V(2).Infof("reconcileResource(): Tuned %s", tuned.Name)

	err = r.syncProfiles(ctx, defaultTuned)
	if err != nil {
		return err
	}

	if tuned.Name == tunedv1.TunedRenderedResourceName {
		// Do not start unused MachineConfig pruning unnecessarily for the rendered resource.
		return nil
	}

	// Tuned CR change can also mean some MachineConfigs the operator created are no longer needed;
	// removal of these will also rollback host settings such as kernel boot parameters.
	err = r.pruneMachineConfigs(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (r *TunedReconciler) getDefaultTuned(ctx context.Context) (*tunedv1.Tuned, error) {
	defaultTuned := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      tunedv1.TunedDefaultResourceName,
	}
	err := r.Get(ctx, key, defaultTuned)
	if err != nil {
		return nil, err
	}
	return defaultTuned, nil
}

func (r *TunedReconciler) syncTunedRendered(ctx context.Context, defaultTuned *tunedv1.Tuned) error {
	tunedList := &tunedv1.TunedList{}
	if err := r.List(ctx, tunedList, client.InNamespace(ntoconfig.OperatorNamespace())); err != nil {
		return fmt.Errorf("failed to list Tuned: %v", err)
	}

	crMf := ntomf.TunedRenderedResource(tunedList)
	crMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(defaultTuned)
	crMf.Name = tunedv1.TunedRenderedResourceName

	// Enable/Disable Pod events based on tuned CRs using this functionality.
	// It is strongly advised not to use the Pod-label functionality in large-scale clusters.
	podLabelsUsed := r.tunedsUsePodLabels(tunedList)
	if r.state.podEventsEnabled != podLabelsUsed {
		r.state.podEventsEnabled = podLabelsUsed
		return fmt.Errorf("Pod events base on Tuned CRs has changed to %v. Returning error and requeing the request", podLabelsUsed)
	}

	cr := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      tunedv1.TunedRenderedResourceName,
	}
	err := r.Get(ctx, key, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncTunedRendered(): Tuned %s not found, creating one", crMf.Name)
			if err := r.Create(ctx, crMf); err != nil {
				return fmt.Errorf("failed to create Tuned %s: %v", crMf.Name, err)
			}
			// Tuned created successfully.
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
	if err := r.Update(ctx, cr); err != nil {
		return fmt.Errorf("failed to update Tuned %s: %v", cr.Name, err)
	}
	klog.Infof("updated Tuned %s", cr.Name)
	return nil
}
func (r *TunedReconciler) syncProfiles(ctx context.Context, tuned *tunedv1.Tuned) error {
	// TODO - test using PartialObjectMetadataList for a reducing client side cache usage
	/*metadataNodeList := metav1.PartialObjectMetadataList{}
	metadataNodeList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "NodeList",
	})
	if err := r.List(ctx, &metadataNodeList); err != nil {
		return fmt.Errorf("failed to list Nodes: %v", err)
	}
	for _, node := range metadataNodeList.Items {
		if err := r.syncProfile(ctx, tuned, node); err != nil {
			return err
		}
	} */

	nodeList := corev1.NodeList{}
	if err := r.List(ctx, &nodeList); err != nil {
		return fmt.Errorf("failed to list Nodes: %v", err)
	}
	nodes := map[string]bool{}
	for _, node := range nodeList.Items {
		if err := r.syncProfile(ctx, tuned, node); err != nil {
			return err
		}
		nodes[node.Name] = true
	}

	profileList := &tunedv1.ProfileList{}
	if err := r.List(ctx, profileList, client.InNamespace(ntoconfig.OperatorNamespace())); err != nil {
		return fmt.Errorf("failed to list Profiles: %v", err)
	}

	// Delete profiles for non existing nodes.
	for _, profile := range profileList.Items {
		if nodes[profile.Name] == false {
			if err := r.Delete(ctx, &profile); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete Profile %s: %v", profile.Name, err)
			}
		}
	}

	return nil
}

func (r *TunedReconciler) syncProfile(ctx context.Context, defaultTuned *tunedv1.Tuned, node corev1.Node) error {
	profileMf := ntomf.TunedProfile()
	profileMf.ObjectMeta.OwnerReferences = getDefaultTunedRefs(defaultTuned)

	profileMf.Name = node.GetName()
	if node.Labels["kubernetes.io/os"] != "linux" {
		klog.Infof("ignoring non-linux Node %s", node.Name)
		return nil
	}
	tunedProfileName, mcLabels, pools, daemonDebug, err := r.calculateProfile(ctx, node)

	if err != nil {
		return err
	}

	metrics.ProfileCalculated(profileMf.Name, tunedProfileName)
	profile := &tunedv1.Profile{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      profileMf.Name,
	}
	err = r.Get(ctx, key, profile)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncProfile(): Profile %s not found, creating one [%s]", profileMf.Name, tunedProfileName)
			profileMf.Spec.Config.TunedProfile = tunedProfileName
			profileMf.Spec.Config.Debug = daemonDebug
			profileMf.Status.Conditions = tunedpkg.InitializeStatusConditions()
			if err := r.Create(ctx, profileMf); err != nil {
				return fmt.Errorf("failed to create Profile %s: %v", profileMf.Name, err)
			}
			// Profile created successfully.
			klog.Infof("created profile %s [%s]", profileMf.Name, tunedProfileName)
			return nil
		}

		return fmt.Errorf("failed to get Profile %s: %v", profileMf.Name, err)
	}

	if mcLabels != nil {
		// The Tuned daemon profile 'tunedProfileName' for nodeName matched with MachineConfig
		// labels set for additional machine configuration.
		// Sync the operator-created MachineConfig for MachineConfigPools 'pools'.
		if profile.Status.TunedProfile == tunedProfileName && profileApplied(profile) {
			// Synchronize MachineConfig only once the (calculated) TuneD profile 'tunedProfileName'
			// has been successfully applied.
			err := r.syncMachineConfig(ctx, getMachineConfigNameForPools(pools), mcLabels, profile.Status.Bootcmdline, profile.Status.Stalld)
			if err != nil {
				return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
			}
		}
	}
	if profile.Spec.Config.TunedProfile == tunedProfileName &&
		profile.Spec.Config.Debug == daemonDebug {
		klog.V(2).Infof("syncProfile(): no need to update Profile %s [%s]", node.GetName(), profile.Spec.Config.TunedProfile)
		return nil
	}
	profile = profile.DeepCopy()
	profile.Spec.Config.TunedProfile = tunedProfileName
	profile.Spec.Config.Debug = daemonDebug
	profile.Status.Conditions = tunedpkg.InitializeStatusConditions()

	klog.V(2).Infof("syncProfile(): updating Profile %s [%s]", profile.Name, tunedProfileName)
	if err := r.Update(ctx, profile); err != nil {
		return fmt.Errorf("failed to update Profile %s: %v", profile.Name, err)
	}
	klog.Infof("updated profile %s [%s]", profile.Name, tunedProfileName)

	return nil
}

func (r *TunedReconciler) syncMachineConfig(ctx context.Context, name string, labels map[string]string, bootcmdline string, stalld *bool) error {
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

	mc := &mcov1.MachineConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	err := r.Get(ctx, key, mc)
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
			if err := r.Create(ctx, mc); err != nil {
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
		// No update needed.
		klog.V(2).Infof("syncMachineConfig(): MachineConfig %s doesn't need updating", mc.ObjectMeta.Name)
		return nil
	}
	mc = mc.DeepCopy()
	mc.Spec.KernelArguments = kernelArguments
	mc.Spec.Config = mcNew.Spec.Config

	l := logline(!ignEq, !kernelArgsEq, bootcmdline)
	klog.V(2).Infof("syncMachineConfig(): updating MachineConfig %s with%s", mc.ObjectMeta.Name, l)
	if err := r.Update(ctx, mc); err != nil {
		return fmt.Errorf("failed to update MachineConfig %s: %v", mc.ObjectMeta.Name, err)
	}
	klog.Infof("updated MachineConfig %s with%s", mc.ObjectMeta.Name, l)

	return nil
}

// pruneMachineConfigs removes any MachineConfigs created by the operator that are not selected by any of the Tuned daemon profile.
func (r *TunedReconciler) pruneMachineConfigs(ctx context.Context) error {
	mcList := &mcov1.MachineConfigList{}
	if err := r.List(ctx, mcList); err != nil {
		return err
	}

	mcNames, err := r.getMachineConfigNamesForTuned(ctx)
	if err != nil {
		return err
	}

	for _, mc := range mcList.Items {
		if mc.ObjectMeta.Annotations != nil {
			if _, ok := mc.ObjectMeta.Annotations[GeneratedByControllerVersionAnnotationKey]; !ok {
				continue
			}
			// mc's annotations have the controller/operator key.

			if mcNames[mc.ObjectMeta.Name] {
				continue
			}
			// This MachineConfig has this operator's annotations and it is not currently used by any
			// Tuned CR; remove it and let MCO roll-back any changes.

			klog.V(2).Infof("pruneMachineConfigs(): deleting MachineConfig %s", mc.ObjectMeta.Name)
			if err := r.Delete(ctx, &mc); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete MachineConfig %s: %v", mc.ObjectMeta.Name, err)
			}
			klog.Infof("deleted MachineConfig %s", mc.ObjectMeta.Name)
		}
	}

	return nil
}

// Get all operator MachineConfig names for all Tuned daemon profiles.
func (r *TunedReconciler) getMachineConfigNamesForTuned(ctx context.Context) (map[string]bool, error) {
	tunedList := &tunedv1.TunedList{}
	if err := r.List(ctx, tunedList, client.InNamespace(ntoconfig.OperatorNamespace())); err != nil {
		return nil, fmt.Errorf("failed to list Tuned: %v", err)
	}

	mcNames := map[string]bool{}

	for _, recommend := range tunedRecommend(tunedList.Items) {
		if recommend.Profile == nil || recommend.MachineConfigLabels == nil {
			continue
		}

		pools, err := r.getPoolsForMachineConfigLabels(recommend.MachineConfigLabels)
		if err != nil {
			return nil, err
		}
		mcName := getMachineConfigNameForPools(pools)

		mcNames[mcName] = true
	}

	return mcNames, nil
}

func (r *TunedReconciler) removeResources(ctx context.Context) error {
	var lastErr error

	dsMf := ntomf.TunedDaemonSet()
	ds := &appsv1.DaemonSet{}
	key := types.NamespacedName{
		Name:      dsMf.Name,
		Namespace: ntoconfig.OperatorNamespace(),
	}

	err := r.Get(ctx, key, ds)
	if err != nil {
		if !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to get DaemonSet %s: %v", dsMf.Name, err)
		}
	} else {
		err = r.Delete(ctx, ds)
		if err != nil && !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to delete DaemonSet %s: %v", dsMf.Name, err)
		} else {
			klog.Infof("deleted DaemonSet %s", dsMf.Name)
		}
	}

	tunedRendered := &tunedv1.Tuned{}
	key = types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      tunedv1.TunedRenderedResourceName,
	}

	err = r.Get(ctx, key, tunedRendered)
	if err != nil {
		if !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		}
	} else {
		err = r.Delete(ctx, tunedRendered)
		if err != nil && !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to delete Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
		} else {
			klog.Infof("deleted Tuned %s", tunedv1.TunedRenderedResourceName)
		}
	}

	listOpts := []client.ListOption{
		client.InNamespace(ntoconfig.OperatorNamespace()),
	}
	profileList := &tunedv1.ProfileList{}
	err = r.List(ctx, profileList, listOpts...)
	if err != nil {
		lastErr = fmt.Errorf("failed to list Tuned Profiles: %v", err)
	}
	for _, profileItem := range profileList.Items {
		r.Delete(ctx, &profileItem)
		if err != nil && !errors.IsNotFound(err) {
			lastErr = fmt.Errorf("failed to delete Profile %s: %v", profileItem.Name, err)
		} else {
			klog.Infof("deleted Profile %s", profileItem.Name)
		}
	}

	err = r.pruneMachineConfigs(ctx)
	if err != nil {
		lastErr = fmt.Errorf("failed to prune operator-created MachineConfigs: %v", err)
	}

	return lastErr
}

func (r *TunedReconciler) validateUpdateEvent(e *event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		klog.Error("update event has no old runtime object to update")
		return false
	}
	if e.ObjectNew == nil {
		klog.Error("update event has no new runtime object for update")
		return false
	}

	return true
}

func getDefaultTunedRefs(tuned *tunedv1.Tuned) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		*metav1.NewControllerRef(tuned, tunedv1.SchemeGroupVersion.WithKind("Tuned")),
	}
}
