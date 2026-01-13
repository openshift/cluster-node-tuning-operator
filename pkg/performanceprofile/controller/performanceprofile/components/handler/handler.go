package handler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
)

var _ components.Handler = &handler{}

type handler struct {
	client.Client
	scheme *runtime.Scheme
}

func NewHandler(cli client.Client, scheme *runtime.Scheme) components.Handler {
	return &handler{Client: cli, scheme: scheme}
}

func (h *handler) Apply(ctx context.Context, obj client.Object, recorder record.EventRecorder, opts *components.Options) error {
	profile, ok := obj.(*performancev2.PerformanceProfile)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=*PerformanceProfile got=%T", obj)
	}

	if profileutil.IsPaused(profile) {
		// this is expected to be exceptional, hence we omit V()
		klog.Infof("Ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil
	}

	// set missing options
	opts.MachineConfig.MixedCPUsEnabled = opts.MixedCPUsFeatureGateEnabled && profileutil.IsMixedCPUsEnabled(profile)

	// First, create components WITHOUT kernel arguments to bootstrap the system.
	// The Tuned CR must be created first so the tuned operand can calculate bootcmdline.
	components, err := manifestset.GetNewComponents(profile, opts)
	if err != nil {
		return err
	}
	for _, componentObj := range components.ToObjects() {
		if err := controllerutil.SetControllerReference(profile, componentObj, h.scheme); err != nil {
			return err
		}
	}

	// get mutated kubelet config
	kcMutated, err := resources.GetMutatedKubeletConfig(ctx, h.Client, components.KubeletConfig)
	if err != nil {
		return err
	}

	// get mutated performance tuned
	performanceTunedMutated, err := resources.GetMutatedTuned(ctx, h.Client, components.Tuned)
	if err != nil {
		return err
	}

	// get mutated RuntimeClass
	runtimeClassMutated, err := resources.GetMutatedRuntimeClass(ctx, h.Client, components.RuntimeClass)
	if err != nil {
		return err
	}

	// Create/Update Tuned, KubeletConfig, and RuntimeClass first (without waiting for bootcmdline)
	if performanceTunedMutated != nil {
		if err := resources.CreateOrUpdateTuned(ctx, h.Client, performanceTunedMutated, profile.Name); err != nil {
			return err
		}
	}

	if kcMutated != nil {
		if err := resources.CreateOrUpdateKubeletConfig(ctx, h.Client, kcMutated); err != nil {
			return err
		}
	}

	if runtimeClassMutated != nil {
		if err := resources.CreateOrUpdateRuntimeClass(ctx, h.Client, runtimeClassMutated); err != nil {
			return err
		}
	}

	// Get kernel arguments from tuned bootcmdline. This will wait for tuned to calculate them.
	kernelArguments, err := resources.GetKernelArgumentsFromTunedBootcmdline(ctx, h.Client, profile)
	if err != nil {
		return err
	}

	// Update options with kernel arguments and create/update MachineConfig
	opts.MachineConfig.KernelArguments = kernelArguments
	if err := h.syncMachineConfig(ctx, profile, opts); err != nil {
		return err
	}

	recorder.Eventf(profile, corev1.EventTypeNormal, "Creation succeeded", "Succeeded to create all components")
	return nil
}

func (h *handler) Delete(ctx context.Context, profileName string) error {
	tunedName := components.GetComponentName(profileName, components.ProfileNamePerformance)
	if err := resources.DeleteTuned(ctx, h.Client, tunedName, components.NamespaceNodeTuningOperator); err != nil {
		return err
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if err := resources.DeleteKubeletConfig(ctx, h.Client, name); err != nil {
		return err
	}
	if err := resources.DeleteRuntimeClass(ctx, h.Client, name); err != nil {
		return err
	}
	if err := resources.DeleteMachineConfig(ctx, h.Client, machineconfig.GetMachineConfigName(profileName)); err != nil {
		return err
	}
	return nil
}

func (h *handler) Exists(ctx context.Context, profileName string) bool {
	tunedName := components.GetComponentName(profileName, components.ProfileNamePerformance)
	if _, err := resources.GetTuned(ctx, h.Client, tunedName, components.NamespaceNodeTuningOperator); !k8serros.IsNotFound(err) {
		klog.V(1).Infof("Tuned %q custom resource is still exists in the namespace %q", tunedName, components.NamespaceNodeTuningOperator)
		return true
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if _, err := resources.GetKubeletConfig(ctx, h.Client, name); !k8serros.IsNotFound(err) {
		klog.V(1).Infof("Kubelet Config %q exists in the cluster", name)
		return true
	}

	if _, err := resources.GetRuntimeClass(ctx, h.Client, name); !k8serros.IsNotFound(err) {
		klog.V(1).Infof("Runtime class %q exists in the cluster", name)
		return true
	}

	if _, err := resources.GetMachineConfig(ctx, h.Client, machineconfig.GetMachineConfigName(profileName)); !k8serros.IsNotFound(err) {
		klog.V(1).Infof("Machine Config %q exists in the cluster", name)
		return true
	}
	return false
}

// syncMachineConfig creates or updates the MachineConfig with the provided kernel arguments
func (h *handler) syncMachineConfig(ctx context.Context, profile *performancev2.PerformanceProfile, opts *components.Options) error {
	// Generate MachineConfig with kernel arguments
	components, err := manifestset.GetNewComponents(profile, opts)
	if err != nil {
		return err
	}

	for _, componentObj := range components.ToObjects() {
		if err := controllerutil.SetControllerReference(profile, componentObj, h.scheme); err != nil {
			return err
		}
	}

	// get mutated machine config with kernel arguments
	mcMutated, err := resources.GetMutatedMachineConfig(ctx, h.Client, components.MachineConfig)
	if err != nil {
		return err
	}

	// Create/Update MachineConfig only after bootcmdline is ready
	if mcMutated != nil {
		if err := resources.CreateOrUpdateMachineConfig(ctx, h.Client, mcMutated); err != nil {
			return err
		}
	}

	return nil
}
