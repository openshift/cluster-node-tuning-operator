package handler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
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
		return fmt.Errorf("wrong type conversion; want=PerformanceProfile got=%T", obj)
	}

	if profileutil.IsPaused(profile) {
		klog.Infof("Ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil
	}

	ctrRuntime, err := h.getContainerRuntimeName(ctx, profile, opts.ProfileMCP)
	if err != nil {
		return fmt.Errorf("could not determine high-performance runtime class container-runtime for profile %q; %w", profile.Name, err)
	}
	klog.Infof("using %q as high-performance runtime class container-runtime for profile %q", ctrRuntime, profile.Name)

	opts.MachineConfig.DefaultRuntime = ctrRuntime
	components, err := manifestset.GetNewComponents(profile, opts)
	if err != nil {
		return err
	}
	for _, componentObj := range components.ToObjects() {
		if err := controllerutil.SetControllerReference(profile, componentObj, h.scheme); err != nil {
			return err
		}
	}

	// get mutated machine config
	mcMutated, err := resources.GetMutatedMachineConfig(ctx, h.Client, components.MachineConfig)
	if err != nil {
		return err
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

	updated := mcMutated != nil ||
		kcMutated != nil ||
		performanceTunedMutated != nil ||
		runtimeClassMutated != nil

	// does not update any resources if it no changes to relevant objects and continue to the status update
	if !updated {
		return nil
	}

	if mcMutated != nil {
		if err := resources.CreateOrUpdateMachineConfig(ctx, h.Client, mcMutated); err != nil {
			return err
		}
	}

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
		klog.Infof("Tuned %q custom resource is still exists in the namespace %q", tunedName, components.NamespaceNodeTuningOperator)
		return true
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if _, err := resources.GetKubeletConfig(ctx, h.Client, name); !k8serros.IsNotFound(err) {
		klog.Infof("Kubelet Config %q exists in the cluster", name)
		return true
	}

	if _, err := resources.GetRuntimeClass(ctx, h.Client, name); !k8serros.IsNotFound(err) {
		klog.Infof("Runtime class %q exists in the cluster", name)
		return true
	}

	if _, err := resources.GetMachineConfig(ctx, h.Client, machineconfig.GetMachineConfigName(profileName)); !k8serros.IsNotFound(err) {
		klog.Infof("Machine Config %q exists in the cluster", name)
		return true
	}
	return false
}

func (h *handler) getContainerRuntimeName(ctx context.Context, profile *performancev2.PerformanceProfile, mcp *mcov1.MachineConfigPool) (mcov1.ContainerRuntimeDefaultRuntime, error) {
	ctrcfgList := &mcov1.ContainerRuntimeConfigList{}
	if err := h.Client.List(ctx, ctrcfgList); err != nil {
		return "", err
	}

	if len(ctrcfgList.Items) == 0 {
		return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	var ctrcfgs []*mcov1.ContainerRuntimeConfig
	mcpSetLabels := labels.Set(mcp.Labels)
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
		klog.Infof("no ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q; using default container runtime", mcpSetLabels.String(), profile.Name)
		return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	if len(ctrcfgs) > 1 {
		return "", fmt.Errorf("more than one ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpSetLabels.String(), profile.Name)
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
