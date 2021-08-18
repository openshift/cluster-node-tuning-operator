/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components/profile"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	nodev1beta1 "k8s.io/api/node/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const finalizer = "foreground-deletion"

// PerformanceProfileReconciler reconciles a PerformanceProfile object
type PerformanceProfileReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	AssetsDir string
}

// SetupWithManager creates a new PerformanceProfile Controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func (r *PerformanceProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {

	// we want to initate reconcile loop only on change under labels or spec of the object
	p := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			return e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() ||
				!apiequality.Semantic.DeepEqual(e.ObjectNew.GetLabels(), e.ObjectOld.GetLabels())
		},
	}

	kubeletPredicates := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			kubeletOld := e.ObjectOld.(*mcov1.KubeletConfig)
			kubeletNew := e.ObjectNew.(*mcov1.KubeletConfig)

			return !reflect.DeepEqual(kubeletOld.Status.Conditions, kubeletNew.Status.Conditions)
		},
	}

	mcpPredicates := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			mcpOld := e.ObjectOld.(*mcov1.MachineConfigPool)
			mcpNew := e.ObjectNew.(*mcov1.MachineConfigPool)

			return !reflect.DeepEqual(mcpOld.Status.Conditions, mcpNew.Status.Conditions)
		},
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&performancev2.PerformanceProfile{}).
		Owns(&mcov1.MachineConfig{}, builder.WithPredicates(p)).
		Owns(&mcov1.KubeletConfig{}, builder.WithPredicates(kubeletPredicates)).
		Owns(&tunedv1.Tuned{}, builder.WithPredicates(p)).
		Owns(&nodev1beta1.RuntimeClass{}, builder.WithPredicates(p)).
		Watches(
			&source.Kind{Type: &mcov1.MachineConfigPool{}},
			handler.EnqueueRequestsFromMapFunc(r.mcpToPerformanceProfile),
			builder.WithPredicates(mcpPredicates)).
		Complete(r)
	if err != nil {
		return err
	}
	return nil
}

func (r *PerformanceProfileReconciler) mcpToPerformanceProfile(mcpObj client.Object) []reconcile.Request {
	mcp := &mcov1.MachineConfigPool{}

	key := types.NamespacedName{
		Namespace: mcpObj.GetNamespace(),
		Name:      mcpObj.GetName(),
	}
	if err := r.Get(context.TODO(), key, mcp); err != nil {
		klog.Errorf("failed to get the machine config pool %+v", key)
		return nil
	}

	profiles := &performancev2.PerformanceProfileList{}
	if err := r.List(context.TODO(), profiles); err != nil {
		klog.Error("failed to get performance profiles")
		return nil
	}

	var requests []reconcile.Request
	for i, profile := range profiles.Items {
		profileNodeSelector := labels.Set(profile.Spec.NodeSelector)
		mcpNodeSelector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse the selector %v", mcp.Spec.NodeSelector)
			return nil
		}

		if mcpNodeSelector.Matches(profileNodeSelector) {
			requests = append(requests, reconcile.Request{NamespacedName: namespacedName(&profiles.Items[i])})
		}
	}

	return requests
}

func validateUpdateEvent(e *event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		klog.Error("Update event has no old runtime object to update")
		return false
	}
	if e.ObjectNew == nil {
		klog.Error("Update event has no new runtime object for update")
		return false
	}

	return true
}

// Reconcile reads that state of the cluster for a PerformanceProfile object and makes changes based on the state read
// and what is in the PerformanceProfile.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PerformanceProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.Info("Reconciling PerformanceProfile")

	// Fetch the PerformanceProfile instance
	instance := &performancev2.PerformanceProfile{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8serros.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if instance.DeletionTimestamp != nil {
		// delete components
		if err := r.deleteComponents(instance); err != nil {
			klog.Errorf("failed to delete components: %v", err)
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Deletion failed", "Failed to delete components: %v", err)
			return reconcile.Result{}, err
		}
		r.Recorder.Eventf(instance, corev1.EventTypeNormal, "Deletion succeeded", "Succeeded to delete all components")

		if r.isComponentsExist(instance) {
			return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// remove finalizer
		if hasFinalizer(instance, finalizer) {
			removeFinalizer(instance, finalizer)
			if err := r.Update(ctx, instance); err != nil {
				return reconcile.Result{}, err
			}

			return reconcile.Result{}, nil
		}
	}

	// add finalizer
	if !hasFinalizer(instance, finalizer) {
		instance.Finalizers = append(instance.Finalizers, finalizer)
		instance.Status.Conditions = r.getProgressingConditions("DeploymentStarting", "Deployment is starting")
		if err := r.Update(ctx, instance); err != nil {
			return reconcile.Result{}, err
		}

		// we exit reconcile loop because we will have additional update reconcile
		return reconcile.Result{}, nil
	}

	profileMCP, err := r.getMachineConfigPoolByProfile(instance)
	if err != nil {
		conditions := r.getDegradedConditions(conditionFailedToFindMachineConfigPool, err.Error())
		if err := r.updateStatus(instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.Name, err)
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	if err := validateProfileMachineConfigPool(instance, profileMCP); err != nil {
		conditions := r.getDegradedConditions(conditionBadMachineConfigLabels, err.Error())
		if err := r.updateStatus(instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.Name, err)
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	// remove components with the old name after the upgrade
	if err := r.deleteDeprecatedComponents(instance); err != nil {
		return ctrl.Result{}, err
	}

	// apply components
	result, err := r.applyComponents(instance, profileMCP)
	if err != nil {
		klog.Errorf("failed to deploy performance profile %q components: %v", instance.Name, err)
		r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Creation failed", "Failed to create all components: %v", err)
		conditions := r.getDegradedConditions(conditionReasonComponentsCreationFailed, err.Error())
		if err := r.updateStatus(instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.Name, err)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	// get kubelet false condition
	conditions, err := r.getKubeletConditionsByProfile(instance)
	if err != nil {
		return r.updateDegradedCondition(instance, conditionFailedGettingKubeletStatus, err)
	}

	// get MCP degraded conditions
	if conditions == nil {
		conditions, err = r.getMCPDegradedCondition(profileMCP)
		if err != nil {
			return r.updateDegradedCondition(instance, conditionFailedGettingMCPStatus, err)
		}
	}

	// get tuned profile degraded conditions
	if conditions == nil {
		conditions, err = r.getTunedConditionsByProfile(instance)
		if err != nil {
			return r.updateDegradedCondition(instance, conditionFailedGettingTunedProfileStatus, err)
		}
	}

	// if conditions were not added due to machine config pool status change then set as available
	if conditions == nil {
		conditions = r.getAvailableConditions()
	}

	if err := r.updateStatus(instance, conditions); err != nil {
		klog.Errorf("failed to update performance profile %q status: %v", instance.Name, err)
		// we still want to requeue after some, also in case of error, to avoid chance of multiple reboots
		if result != nil {
			return *result, nil
		}

		return reconcile.Result{}, err
	}

	if result != nil {
		return *result, nil
	}

	return ctrl.Result{}, nil
}

func (r *PerformanceProfileReconciler) deleteDeprecatedComponents(instance *performancev2.PerformanceProfile) error {
	// remove the machine config with the deprecated name
	name := components.GetComponentName(instance.Name, components.ComponentNamePrefix)
	return r.deleteMachineConfig(name)
}

func (r *PerformanceProfileReconciler) updateDegradedCondition(instance *performancev2.PerformanceProfile, conditionState string, conditionError error) (ctrl.Result, error) {
	conditions := r.getDegradedConditions(conditionState, conditionError.Error())
	if err := r.updateStatus(instance, conditions); err != nil {
		klog.Errorf("failed to update performance profile %q status: %v", instance.Name, err)
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, conditionError
}

func (r *PerformanceProfileReconciler) applyComponents(profile *performancev2.PerformanceProfile, profileMCP *mcov1.MachineConfigPool) (*reconcile.Result, error) {
	if profileutil.IsPaused(profile) {
		klog.Infof("Ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil, nil
	}

	components, err := manifestset.GetNewComponents(profile, profileMCP, &r.AssetsDir)
	if err != nil {
		return nil, err
	}
	for _, componentObj := range components.ToObjects() {
		if err := controllerutil.SetControllerReference(profile, componentObj, r.Scheme); err != nil {
			return nil, err
		}
	}

	// get mutated machine config
	mcMutated, err := r.getMutatedMachineConfig(components.MachineConfig)
	if err != nil {
		return nil, err
	}

	// get mutated kubelet config
	kcMutated, err := r.getMutatedKubeletConfig(components.KubeletConfig)
	if err != nil {
		return nil, err
	}

	// get mutated performance tuned
	performanceTunedMutated, err := r.getMutatedTuned(components.Tuned)
	if err != nil {
		return nil, err
	}

	// get mutated RuntimeClass
	runtimeClassMutated, err := r.getMutatedRuntimeClass(components.RuntimeClass)
	if err != nil {
		return nil, err
	}

	updated := mcMutated != nil ||
		kcMutated != nil ||
		performanceTunedMutated != nil ||
		runtimeClassMutated != nil

	// does not update any resources, if it no changes to relevant objects and just continue to the status update
	if !updated {
		return nil, nil
	}

	if mcMutated != nil {
		if err := r.createOrUpdateMachineConfig(mcMutated); err != nil {
			return nil, err
		}
	}

	if performanceTunedMutated != nil {
		if err := r.createOrUpdateTuned(performanceTunedMutated, profile.Name); err != nil {
			return nil, err
		}
	}

	if kcMutated != nil {
		if err := r.createOrUpdateKubeletConfig(kcMutated); err != nil {
			return nil, err
		}
	}

	if runtimeClassMutated != nil {
		if err := r.createOrUpdateRuntimeClass(runtimeClassMutated); err != nil {
			return nil, err
		}
	}

	r.Recorder.Eventf(profile, corev1.EventTypeNormal, "Creation succeeded", "Succeeded to create all components")
	return &reconcile.Result{}, nil
}

func (r *PerformanceProfileReconciler) deleteComponents(profile *performancev2.PerformanceProfile) error {
	tunedName := components.GetComponentName(profile.Name, components.ProfileNamePerformance)
	if err := r.deleteTuned(tunedName, components.NamespaceNodeTuningOperator); err != nil {
		return err
	}

	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	if err := r.deleteKubeletConfig(name); err != nil {
		return err
	}

	if err := r.deleteRuntimeClass(name); err != nil {
		return err
	}

	if err := r.deleteMachineConfig(machineconfig.GetMachineConfigName(profile)); err != nil {
		return err
	}

	return nil

}

func (r *PerformanceProfileReconciler) isComponentsExist(profile *performancev2.PerformanceProfile) bool {
	tunedName := components.GetComponentName(profile.Name, components.ProfileNamePerformance)
	if _, err := r.getTuned(tunedName, components.NamespaceNodeTuningOperator); !k8serros.IsNotFound(err) {
		klog.Infof("Tuned %q custom resource is still exists under the namespace %q", tunedName, components.NamespaceNodeTuningOperator)
		return true
	}

	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	if _, err := r.getKubeletConfig(name); !k8serros.IsNotFound(err) {
		klog.Infof("Kubelet Config %q exists under the cluster", name)
		return true
	}

	if _, err := r.getRuntimeClass(name); !k8serros.IsNotFound(err) {
		klog.Infof("Runtime class %q exists under the cluster", name)
		return true
	}

	if _, err := r.getMachineConfig(machineconfig.GetMachineConfigName(profile)); !k8serros.IsNotFound(err) {
		klog.Infof("Machine Config %q exists under the cluster", name)
		return true
	}

	return false
}

func hasFinalizer(profile *performancev2.PerformanceProfile, finalizer string) bool {
	for _, f := range profile.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

func removeFinalizer(profile *performancev2.PerformanceProfile, finalizer string) {
	var finalizers []string
	for _, f := range profile.Finalizers {
		if f == finalizer {
			continue
		}
		finalizers = append(finalizers, f)
	}
	profile.Finalizers = finalizers
}

func namespacedName(obj metav1.Object) types.NamespacedName {
	return types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}

func (r *PerformanceProfileReconciler) getMachineConfigPoolByProfile(profile *performancev2.PerformanceProfile) (*mcov1.MachineConfigPool, error) {
	nodeSelector := labels.Set(profile.Spec.NodeSelector)

	mcpList := &mcov1.MachineConfigPoolList{}
	if err := r.Client.List(context.TODO(), mcpList); err != nil {
		return nil, err
	}

	filteredMCPList := filterMCPDuplications(mcpList.Items)

	var profileMCPs []*mcov1.MachineConfigPool
	for i := range filteredMCPList {
		mcp := &mcpList.Items[i]

		if mcp.Spec.NodeSelector == nil {
			continue
		}

		mcpNodeSelector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
		if err != nil {
			return nil, err
		}

		if mcpNodeSelector.Matches(nodeSelector) {
			profileMCPs = append(profileMCPs, mcp)
		}
	}

	if len(profileMCPs) == 0 {
		return nil, fmt.Errorf("failed to find MCP with the node selector that matches labels %q", nodeSelector.String())
	}

	if len(profileMCPs) > 1 {
		return nil, fmt.Errorf("more than one MCP found that matches performance profile node selector %q", nodeSelector.String())
	}

	return profileMCPs[0], nil
}

func filterMCPDuplications(mcps []mcov1.MachineConfigPool) []mcov1.MachineConfigPool {
	var filtered []mcov1.MachineConfigPool
	items := map[string]mcov1.MachineConfigPool{}
	for _, mcp := range mcps {
		if _, exists := items[mcp.Name]; !exists {
			items[mcp.Name] = mcp
			filtered = append(filtered, mcp)
		}
	}

	return filtered
}

func validateProfileMachineConfigPool(profile *performancev2.PerformanceProfile, profileMCP *mcov1.MachineConfigPool) error {
	if profileMCP.Spec.MachineConfigSelector.Size() == 0 {
		return fmt.Errorf("the MachineConfigPool %q machineConfigSelector is nil", profileMCP.Name)
	}

	if len(profileMCP.Labels) == 0 {
		return fmt.Errorf("the MachineConfigPool %q does not have any labels that can be used to bind it together with KubeletConfing", profileMCP.Name)
	}

	// we can not guarantee that our generated label for the machine config selector will be the right one
	// but at least we can validate that the MCP will consume our machine config
	machineConfigLabels := profileutil.GetMachineConfigLabel(profile)
	mcpMachineConfigSelector, err := metav1.LabelSelectorAsSelector(profileMCP.Spec.MachineConfigSelector)
	if err != nil {
		return err
	}

	if !mcpMachineConfigSelector.Matches(labels.Set(machineConfigLabels)) {
		if len(profile.Spec.MachineConfigLabel) > 0 {
			return fmt.Errorf("the machine config labels %v provided via profile.spec.machineConfigLabel do not match the MachineConfigPool %q machineConfigSelector %q", machineConfigLabels, profileMCP.Name, mcpMachineConfigSelector.String())
		}

		return fmt.Errorf("the machine config labels %v generated from the profile.spec.nodeSelector %v do not match the MachineConfigPool %q machineConfigSelector %q", machineConfigLabels, profile.Spec.NodeSelector, profileMCP.Name, mcpMachineConfigSelector.String())
	}

	return nil
}
