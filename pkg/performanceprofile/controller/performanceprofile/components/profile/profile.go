package profile

import (
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

// GetMachineConfigPoolSelector returns the MachineConfigPoolSelector from the CR or a default value calculated based on NodeSelector
func GetMachineConfigPoolSelector(profile *performancev2.PerformanceProfile, profileMCP *mcov1.MachineConfigPool) map[string]string {
	// we do not really need profile.spec.machineConfigPoolSelector anymore, but we should use it for backward compatibility
	if profile.Spec.MachineConfigPoolSelector != nil {
		return profile.Spec.MachineConfigPoolSelector
	}

	if profileMCP != nil {
		return profileMCP.Labels
	}

	// we still need to construct the machineConfigPoolSelector when the command called from the render command
	return getDefaultLabel(profile)
}

// GetMachineConfigLabel returns the MachineConfigLabels from the CR or a default value calculated based on NodeSelector
func GetMachineConfigLabel(profile *performancev2.PerformanceProfile) map[string]string {
	if profile.Spec.MachineConfigLabel != nil {
		return profile.Spec.MachineConfigLabel
	}

	return getDefaultLabel(profile)
}

func getDefaultLabel(profile *performancev2.PerformanceProfile) map[string]string {
	nodeSelectorKey, _ := components.GetFirstKeyAndValue(profile.Spec.NodeSelector)
	// no error handling needed, it's validated already
	_, nodeRole, _ := components.SplitLabelKey(nodeSelectorKey)

	labels := make(map[string]string)
	labels[components.MachineConfigRoleLabelKey] = nodeRole

	return labels
}

// IsPaused returns whether or not a performance profile's reconcile loop is paused
func IsPaused(profile *performancev2.PerformanceProfile) bool {
	if profile.Annotations == nil {
		return false
	}

	isPaused, ok := profile.Annotations[performancev2.PerformanceProfilePauseAnnotation]
	if ok && isPaused == "true" {
		return true
	}

	return false
}

// IsCgroupsVersionIgnored returns whether or not the performance profile's cgroup
// downgrade logic should be executed
func IsCgroupsVersionIgnored(profile *performancev2.PerformanceProfile) bool {
	if profile.Annotations == nil {
		return false
	}

	isIgnored, ok := profile.Annotations[performancev2.PerformanceProfileIgnoreCgroupsVersion]
	if ok && isIgnored == "true" {
		return true
	}

	return false
}

// IsPhysicalRpsEnabled checks if RPS mask should be set for all physical net devices
func IsPhysicalRpsEnabled(profile *performancev2.PerformanceProfile) bool {
	if profile.Annotations == nil {
		return false
	}
	IsPhysicalRpsEnabled, ok := profile.Annotations[performancev2.PerformanceProfileEnablePhysicalRpsAnnotation]
	if ok && IsPhysicalRpsEnabled == "true" {
		return true
	}

	return false
}

// IsRpsEnabled checks if all RPS should be applied
func IsRpsEnabled(profile *performancev2.PerformanceProfile) bool {
	if profile.Annotations == nil {
		return false
	}
	isRpsEnabled, ok := profile.Annotations[performancev2.PerformanceProfileEnableRpsAnnotation]
	if ok && isRpsEnabled == "true" {
		return true
	}

	return false
}
