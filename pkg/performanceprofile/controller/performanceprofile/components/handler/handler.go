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
	ntosync "github.com/openshift/cluster-node-tuning-operator/pkg/sync"
)

var _ components.Handler = &handler{}

// BootcmdlineNotReadyError is returned when the bootcmdline sync signal hasn't been received yet.
// This error indicates that the PerformanceProfile controller should requeue and wait for
// the operator controller to signal that bootcmdline is ready.
// The operator controller will trigger immediate reconciliation when ready, so the requeue
// interval serves only as a fallback.
type BootcmdlineNotReadyError struct {
	MCPName string
	Message string
}

func (e *BootcmdlineNotReadyError) Error() string {
	return e.Message
}

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
	performanceTuned, performanceTunedNeedsUpdate, err := resources.GetMutatedTuned(ctx, h.Client, components.Tuned)
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
		performanceTunedNeedsUpdate ||
		runtimeClassMutated != nil

	// does not update any resources if it no changes to relevant objects and continue to the status update
	if !updated {
		return nil
	}

	if opts.ProfileMCP == nil {
		// This can happen when MCP validation fails.
		return fmt.Errorf("no valid PerformanceProfile's MCP")
	}

	// Create/update Tuned FIRST - TuneD pods need this to calculate bootcmdline
	if performanceTunedNeedsUpdate {
		ntosync.GetBootcmdlineSync().ClearCacheForPool(opts.ProfileMCP.Name)
		if err := resources.CreateOrUpdateTuned(ctx, h.Client, performanceTuned, profile.Name); err != nil {
			return err
		}
		// No point in checking any further, performanceTuned.Generation will not match below.
		// Wait for explicit reconcileTrigger or periodic resync.
		return nil
	}

	// Check bootcmdline sync signal from the operator controller before creating MachineConfig.
	// This synchronizes the creation of the PerformanceProfile's MachineConfig (50-performance-*)
	// with the operator's MachineConfig (50-nto-*) to prevent race conditions.
	// The check is generation-aware: it verifies that the bootcmdline was calculated with the
	// current performance Tuned CR (identified by name:generation). This prevents races where the
	// operator controller might signal ready for an older generation of the Tuned CR.
	// The check is non-blocking; if not ready, we return an error to requeue.
	// The operator controller will trigger immediate reconciliation when ready.
	if mcMutated != nil {
		mcpName := opts.ProfileMCP.Name
		bootcmdlineSync := ntosync.GetBootcmdlineSync()

		// Check if the current performance Tuned CR is included in the bootcmdline calculation.
		// We use the performanceTuned object which always contains the existing CR (with Generation field)
		// whether or not it needs updating.
		expectedTunedDep := fmt.Sprintf("%s:%d", performanceTuned.Name, performanceTuned.Generation)

		// Non-blocking generation-aware check - verify the operator has processed this Tuned generation
		if !bootcmdlineSync.IsReady(mcpName, expectedTunedDep) {
			klog.Infof("PerformanceProfile %q: bootcmdline not ready for MCP %q, waiting for operator controller signal", profile.Name, mcpName)
			return &BootcmdlineNotReadyError{
				MCPName: mcpName,
				Message: fmt.Sprintf("waiting for bootcmdline to be ready for MCP %q", mcpName),
			}
		}

		klog.Infof("PerformanceProfile %q: bootcmdline ready for MCP %q, creating MachineConfig", profile.Name, mcpName)
		if err := resources.CreateOrUpdateMachineConfig(ctx, h.Client, mcMutated); err != nil {
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
