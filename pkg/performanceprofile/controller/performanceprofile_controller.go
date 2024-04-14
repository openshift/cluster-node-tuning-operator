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

package controller

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"

	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const finalizer = "foreground-deletion"

// PerformanceProfileReconciler reconciles a PerformanceProfile object
type PerformanceProfileReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	FeatureGate featuregates.FeatureGate
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

			return kubeletOld.GetGeneration() != kubeletNew.GetGeneration() ||
				!reflect.DeepEqual(kubeletOld.Status.Conditions, kubeletNew.Status.Conditions)
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

	tunedProfilePredicates := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			tunedProfileOld := e.ObjectOld.(*tunedv1.Profile)
			tunedProfileNew := e.ObjectNew.(*tunedv1.Profile)

			return !reflect.DeepEqual(tunedProfileOld.Status.Conditions, tunedProfileNew.Status.Conditions)
		},
	}

	ctrcfgPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			ctrcfg := e.Object.(*mcov1.ContainerRuntimeConfig)
			return ctrcfg.Spec.ContainerRuntimeConfig.DefaultRuntime != ""
		},

		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			ctrcfgOld := e.ObjectOld.(*mcov1.ContainerRuntimeConfig)
			ctrcfgNew := e.ObjectNew.(*mcov1.ContainerRuntimeConfig)
			return !reflect.DeepEqual(ctrcfgOld.Status.Conditions, ctrcfgNew.Status.Conditions)
		},

		DeleteFunc: func(e event.DeleteEvent) bool {
			ctrcfg := e.Object.(*mcov1.ContainerRuntimeConfig)
			return ctrcfg.Spec.ContainerRuntimeConfig.DefaultRuntime != ""
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&performancev2.PerformanceProfile{}).
		Owns(&mcov1.MachineConfig{}, builder.WithPredicates(p)).
		Owns(&mcov1.KubeletConfig{}, builder.WithPredicates(kubeletPredicates)).
		Owns(&tunedv1.Tuned{}, builder.WithPredicates(p)).
		Owns(&nodev1.RuntimeClass{}, builder.WithPredicates(p)).
		Watches(&mcov1.MachineConfigPool{},
			handler.EnqueueRequestsFromMapFunc(r.mcpToPerformanceProfile),
			builder.WithPredicates(mcpPredicates)).
		Watches(&tunedv1.Profile{},
			handler.EnqueueRequestsFromMapFunc(r.tunedProfileToPerformanceProfile),
			builder.WithPredicates(tunedProfilePredicates),
		).
		Watches(&mcov1.ContainerRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.ctrRuntimeConfToPerformanceProfile),
			builder.WithPredicates(ctrcfgPredicates)).
		Complete(r)
}

func (r *PerformanceProfileReconciler) mcpToPerformanceProfile(ctx context.Context, mcpObj client.Object) []reconcile.Request {
	mcp := &mcov1.MachineConfigPool{}

	key := types.NamespacedName{
		Namespace: mcpObj.GetNamespace(),
		Name:      mcpObj.GetName(),
	}
	if err := r.Get(ctx, key, mcp); err != nil {
		klog.Errorf("failed to get the machine config pool %+v: %v", key, err)
		return nil
	}

	profiles := &performancev2.PerformanceProfileList{}
	if err := r.List(context.TODO(), profiles); err != nil {
		klog.Errorf("failed to get performance profiles: %v", err)
		return nil
	}

	return mcpToPerformanceProfileReconcileRequests(profiles, mcp)
}

func mcpToPerformanceProfileReconcileRequests(profiles *performancev2.PerformanceProfileList, mcp *mcov1.MachineConfigPool) []reconcile.Request {
	var requests []reconcile.Request
	for i, profile := range profiles.Items {
		profileNodeSelector := labels.Set(profile.Spec.NodeSelector)
		mcpNodeSelector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse the selector %v: %v", mcp.Spec.NodeSelector, err)
			return nil
		}

		if mcpNodeSelector.Matches(profileNodeSelector) {
			requests = append(requests, reconcile.Request{NamespacedName: namespacedName(&profiles.Items[i])})
		}
	}

	return requests
}

func (r *PerformanceProfileReconciler) tunedProfileToPerformanceProfile(ctx context.Context, tunedProfileObj client.Object) []reconcile.Request {
	node := &corev1.Node{}
	key := types.NamespacedName{
		// the tuned profile name is the same as node
		Name: tunedProfileObj.GetName(),
	}

	if err := r.Get(ctx, key, node); err != nil {
		klog.Errorf("failed to get the tuned profile %+v: %v", key, err)
		return nil
	}

	profiles := &performancev2.PerformanceProfileList{}
	if err := r.List(context.TODO(), profiles); err != nil {
		klog.Errorf("failed to get performance profiles: %v", err)
		return nil
	}

	var requests []reconcile.Request
	for i, profile := range profiles.Items {
		profileNodeSelector := labels.Set(profile.Spec.NodeSelector)
		nodeLabels := labels.Set(node.Labels)
		if profileNodeSelector.AsSelector().Matches(nodeLabels) {
			requests = append(requests, reconcile.Request{NamespacedName: namespacedName(&profiles.Items[i])})
		}
	}

	return requests
}

func (r *PerformanceProfileReconciler) ctrRuntimeConfToPerformanceProfile(ctx context.Context, ctrRuntimeConfObj client.Object) []reconcile.Request {
	ctrcfg := &mcov1.ContainerRuntimeConfig{}

	err := r.Get(ctx, client.ObjectKeyFromObject(ctrRuntimeConfObj), ctrcfg)
	if err != nil {
		klog.Errorf("failed to get container runtime config; name=%q err=%v", ctrRuntimeConfObj.GetName(), err)
		return nil
	}
	klog.Infof("reconciling from ContainerRuntimeConfig %q", ctrcfg.Name)

	selector, err := metav1.LabelSelectorAsSelector(ctrcfg.Spec.MachineConfigPoolSelector)
	if err != nil {
		klog.Errorf("failed to parse the selector %v for container runtime config; name=%q err=%v", ctrcfg.Spec.MachineConfigPoolSelector, ctrRuntimeConfObj.GetName(), err)
		return nil
	}

	mcps := &mcov1.MachineConfigPoolList{}
	opts := &client.ListOptions{
		LabelSelector: selector,
	}

	err = r.List(ctx, mcps, opts)
	if err != nil {
		klog.Errorf("failed to list machine config pools; err=%v", err)
		return nil
	}

	klog.Infof("reconciling from ContainerRuntimeConfig %q selector %v: %d MCPs", ctrcfg.Name, ctrcfg.Spec.MachineConfigPoolSelector, len(mcps.Items))

	profiles := &performancev2.PerformanceProfileList{}
	err = r.List(ctx, profiles)
	if err != nil {
		klog.Errorf("failed to get performance profiles: %v", err)
		return nil
	}

	var allRequests []reconcile.Request
	for i := 0; i < len(mcps.Items); i++ {
		requests := mcpToPerformanceProfileReconcileRequests(profiles, &mcps.Items[i])
		if requests == nil {
			return nil
		}
		allRequests = append(allRequests, requests...)
	}
	return allRequests
}

func (r *PerformanceProfileReconciler) getInfraPartitioningMode() (pinning apiconfigv1.CPUPartitioningMode, err error) {
	key := types.NamespacedName{
		Name: "cluster",
	}
	infra := &apiconfigv1.Infrastructure{}

	if err = r.Client.Get(context.Background(), key, infra); err != nil {
		return
	}

	return infra.Status.CPUPartitioning, nil
}

func getContainerRuntimeName(ctx context.Context, client client.Client, mcpLabels map[string]string, profileName string) (mcov1.ContainerRuntimeDefaultRuntime, error) {
	ctrcfgList := &mcov1.ContainerRuntimeConfigList{}
	if err := client.List(ctx, ctrcfgList); err != nil {
		return "", err
	}

	if len(ctrcfgList.Items) == 0 {
		return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	var ctrcfgs []*mcov1.ContainerRuntimeConfig
	mcpSetLabels := labels.Set(mcpLabels)
	for i := 0; i < len(ctrcfgList.Items); i++ {
		ctrcfg := &ctrcfgList.Items[i]
		ctrcfgSelector, err := metav1.LabelSelectorAsSelector(ctrcfg.Spec.MachineConfigPoolSelector)
		if err != nil {
			return "", err
		}
		if ctrcfgSelector.Matches(mcpSetLabels) {
			ctrcfgs = append(ctrcfgs, ctrcfg)
		}
	}

	if len(ctrcfgs) == 0 {
		klog.Infof("no ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q; using default container runtime", mcpSetLabels.String(), profileName)
		return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	if len(ctrcfgs) > 1 {
		return "", fmt.Errorf("more than one ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpSetLabels.String(), profileName)
	}

	condition := status.GetLatestContainerRuntimeConfigCondition(ctrcfgs[0].Status.Conditions)
	if condition == nil {
		return "", fmt.Errorf("ContainerRuntimeConfig: %q no conditions reported (yet)", ctrcfgs[0].Name)
	}
	if condition.Type != mcov1.ContainerRuntimeConfigSuccess || condition.Status != corev1.ConditionTrue {
		return "", fmt.Errorf("ContainerRuntimeConfig: %q failed to be applied: message=%q", ctrcfgs[0].Name, condition.Message)
	}
	return ctrcfgs[0].Spec.ContainerRuntimeConfig.DefaultRuntime, nil
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

// +kubebuilder:rbac:groups="",resources=events,verbs=*
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=performance.openshift.io,resources=performanceprofiles;performanceprofiles/status;performanceprofiles/finalizers,verbs=*
// +kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigs;kubeletconfigs,verbs=*
// +kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools;containerruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=tuned.openshift.io,resources=tuneds;profiles,verbs=*
// +kubebuilder:rbac:groups=node.k8s.io,resources=runtimeclasses,verbs=*
// +kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
// +kubebuilder:rbac:namespace="openshift-cluster-node-tuning-operator",groups=core,resources=pods;services;services/finalizers;configmaps,verbs=*
// +kubebuilder:rbac:namespace="openshift-cluster-node-tuning-operator",groups=coordination.k8s.io,resources=leases,verbs=create;get;list;update
// +kubebuilder:rbac:namespace="openshift-cluster-node-tuning-operator",groups=apps,resourceNames=performance-operator,resources=deployments/finalizers,verbs=update
// +kubebuilder:rbac:namespace="openshift-cluster-node-tuning-operator",groups=monitoring.coreos.com,resources=servicemonitors,verbs=*

// Reconcile reads that state of the cluster for a PerformanceProfile object and makes changes based on the state read
// and what is in the PerformanceProfile.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PerformanceProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	co, err := resources.GetClusterOperator(ctx, r.Client)
	if err != nil {
		klog.Errorf("failed to get ClusterOperator: %v", err)
		return reconcile.Result{}, err
	}

	operatorReleaseVersion := os.Getenv("RELEASE_VERSION")
	operandReleaseVersion := operatorv1helpers.FindOperandVersion(co.Status.Versions, tunedv1.TunedOperandName)
	if operandReleaseVersion == nil || operatorReleaseVersion != operandReleaseVersion.Version {
		// Upgrade in progress
		klog.Infof("operator and operand release versions do not match")
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	klog.Info("Reconciling PerformanceProfile")
	// Fetch the PerformanceProfile instance
	instance := &performancev2.PerformanceProfile{}
	err = r.Get(ctx, req.NamespacedName, instance)
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
		if err := r.deleteComponents(instance.GetName()); err != nil {
			klog.Errorf("failed to delete components: %v", err)
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Deletion failed", "Failed to delete components: %v", err)
			return reconcile.Result{}, err
		}
		r.Recorder.Eventf(instance, corev1.EventTypeNormal, "Deletion succeeded", "Succeeded to delete all components")

		if r.isComponentsExist(instance.GetName()) {
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
		instance.Status.Conditions = status.GetProgressingConditions("DeploymentStarting", "Deployment is starting")
		if err := r.Update(ctx, instance); err != nil {
			return reconcile.Result{}, err
		}

		// we exit reconcile loop because we will have additional update reconcile
		return reconcile.Result{}, nil
	}

	pinningMode, err := r.getInfraPartitioningMode()
	if err != nil {
		return ctrl.Result{}, err
	}

	profileMCP, err := getMachineConfigPoolByProfile(ctx, r.Client, instance)
	if err != nil {
		conditions := status.GetDegradedConditions(status.ConditionFailedToFindMachineConfigPool, err.Error())
		if err := status.Update(ctx, r.Client, instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	ctrRuntime, err := getContainerRuntimeName(ctx, r.Client, profileMCP.Labels, instance.GetName())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not determine high-performance runtime class container-runtime for profile %q; %w", instance.GetName(), err)
	}
	klog.Infof("using %q as high-performance runtime class container-runtime for profile %q", ctrRuntime, instance.GetName())

	if err := validateProfileMachineConfigPool(instance, profileMCP); err != nil {
		conditions := status.GetDegradedConditions(status.ConditionBadMachineConfigLabels, err.Error())
		if err := status.Update(ctx, r.Client, instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	// apply components
	result, err := r.applyComponents(instance, &components.Options{
		ProfileMCP: profileMCP,
		MachineConfig: components.MachineConfigOptions{
			PinningMode:      &pinningMode,
			DefaultRuntime:   ctrRuntime,
			MixedCPUsEnabled: r.isMixedCPUsEnabled(instance),
		},
	})
	if err != nil {
		klog.Errorf("failed to deploy performance profile %q components: %v", instance.GetName(), err)
		r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Creation failed", "Failed to create all components: %v", err)
		conditions := status.GetDegradedConditions(status.ConditionReasonComponentsCreationFailed, err.Error())
		if err := status.Update(ctx, r.Client, instance, conditions); err != nil {
			klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	// get kubelet false condition
	conditions, err := status.GetKubeletConditionsByProfile(ctx, r.Client, instance.GetName())
	if err != nil {
		return r.updateDegradedCondition(instance, status.ConditionFailedGettingKubeletStatus, err)
	}

	// get MCP degraded conditions
	if conditions == nil {
		conditions, err = status.GetMCPDegradedCondition(profileMCP)
		if err != nil {
			return r.updateDegradedCondition(instance, status.ConditionFailedGettingMCPStatus, err)
		}
	}

	// get tuned profile degraded conditions
	if conditions == nil {
		conditions, err = status.GetTunedConditionsByProfile(ctx, r.Client, instance)
		if err != nil {
			return r.updateDegradedCondition(instance, status.ConditionFailedGettingTunedProfileStatus, err)
		}
	}

	// if conditions were not added due to machine config pool status change then set as available
	if conditions == nil {
		conditions = status.GetAvailableConditions("")
	}

	if err := status.Update(ctx, r.Client, instance, conditions); err != nil {
		klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
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

func (r *PerformanceProfileReconciler) updateDegradedCondition(instance *performancev2.PerformanceProfile, conditionState string, conditionError error) (ctrl.Result, error) {
	conditions := status.GetDegradedConditions(conditionState, conditionError.Error())
	if err := status.Update(context.TODO(), r.Client, instance, conditions); err != nil {
		klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, conditionError
}

func (r *PerformanceProfileReconciler) applyComponents(profile *performancev2.PerformanceProfile, opts *components.Options) (*reconcile.Result, error) {
	if profileutil.IsPaused(profile) {
		klog.Infof("Ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil, nil
	}
	components, err := manifestset.GetNewComponents(profile, opts)
	if err != nil {
		return nil, err
	}
	for _, componentObj := range components.ToObjects() {
		if err := controllerutil.SetControllerReference(profile, componentObj, r.Scheme); err != nil {
			return nil, err
		}
	}

	// get mutated machine config
	mcMutated, err := resources.GetMutatedMachineConfig(context.TODO(), r.Client, components.MachineConfig)
	if err != nil {
		return nil, err
	}

	// get mutated kubelet config
	kcMutated, err := resources.GetMutatedKubeletConfig(context.TODO(), r.Client, components.KubeletConfig)
	if err != nil {
		return nil, err
	}

	// get mutated performance tuned
	performanceTunedMutated, err := resources.GetMutatedTuned(context.TODO(), r.Client, components.Tuned)
	if err != nil {
		return nil, err
	}

	// get mutated RuntimeClass
	runtimeClassMutated, err := resources.GetMutatedRuntimeClass(context.TODO(), r.Client, components.RuntimeClass)
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
		if err := resources.CreateOrUpdateMachineConfig(context.TODO(), r.Client, mcMutated); err != nil {
			return nil, err
		}
	}

	if performanceTunedMutated != nil {
		if err := resources.CreateOrUpdateTuned(context.TODO(), r.Client, performanceTunedMutated, profile.Name); err != nil {
			return nil, err
		}
	}

	if kcMutated != nil {
		if err := resources.CreateOrUpdateKubeletConfig(context.TODO(), r.Client, kcMutated); err != nil {
			return nil, err
		}
	}

	if runtimeClassMutated != nil {
		if err := resources.CreateOrUpdateRuntimeClass(context.TODO(), r.Client, runtimeClassMutated); err != nil {
			return nil, err
		}
	}

	r.Recorder.Eventf(profile, corev1.EventTypeNormal, "Creation succeeded", "Succeeded to create all components")
	return &reconcile.Result{}, nil
}

func (r *PerformanceProfileReconciler) deleteComponents(profileName string) error {
	tunedName := components.GetComponentName(profileName, components.ProfileNamePerformance)
	if err := resources.DeleteTuned(context.TODO(), r.Client, tunedName, components.NamespaceNodeTuningOperator); err != nil {
		return err
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if err := resources.DeleteKubeletConfig(context.TODO(), r.Client, name); err != nil {
		return err
	}

	if err := resources.DeleteRuntimeClass(context.TODO(), r.Client, name); err != nil {
		return err
	}

	if err := resources.DeleteMachineConfig(context.TODO(), r.Client, machineconfig.GetMachineConfigName(profileName)); err != nil {
		return err
	}

	return nil
}

func (r *PerformanceProfileReconciler) isComponentsExist(profileName string) bool {
	tunedName := components.GetComponentName(profileName, components.ProfileNamePerformance)
	if _, err := resources.GetTuned(context.TODO(), r.Client, tunedName, components.NamespaceNodeTuningOperator); !k8serros.IsNotFound(err) {
		klog.Infof("Tuned %q custom resource is still exists under the namespace %q", tunedName, components.NamespaceNodeTuningOperator)
		return true
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if _, err := resources.GetKubeletConfig(context.TODO(), r.Client, name); !k8serros.IsNotFound(err) {
		klog.Infof("Kubelet Config %q exists under the cluster", name)
		return true
	}

	if _, err := resources.GetRuntimeClass(context.TODO(), r.Client, name); !k8serros.IsNotFound(err) {
		klog.Infof("Runtime class %q exists under the cluster", name)
		return true
	}

	if _, err := resources.GetMachineConfig(context.TODO(), r.Client, machineconfig.GetMachineConfigName(profileName)); !k8serros.IsNotFound(err) {
		klog.Infof("Machine Config %q exists under the cluster", name)
		return true
	}

	return false
}

func (r *PerformanceProfileReconciler) isMixedCPUsEnabled(profile *performancev2.PerformanceProfile) bool {
	if !r.FeatureGate.Enabled(apiconfigv1.FeatureGateMixedCPUsAllocation) {
		return false
	}
	if config.InHyperShift() {
		return false
	}
	return profileutil.IsMixedCPUsEnabled(profile)
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

func getMachineConfigPoolByProfile(ctx context.Context, client client.Client, profile *performancev2.PerformanceProfile) (*mcov1.MachineConfigPool, error) {
	nodeSelector := labels.Set(profile.Spec.NodeSelector)

	mcpList := &mcov1.MachineConfigPoolList{}
	if err := client.List(ctx, mcpList); err != nil {
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
