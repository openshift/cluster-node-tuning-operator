package status

import (
	"bytes"
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	nto "github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

const (
	ConditionFailedToFindMachineConfigPool   = "GettingMachineConfigPoolFailed"
	ConditionBadMachineConfigLabels          = "BadMachineConfigLabels"
	ConditionReasonComponentsCreationFailed  = "ComponentCreationFailed"
	ConditionReasonMCPDegraded               = "MCPDegraded"
	ConditionFailedGettingMCPStatus          = "GettingMCPStatusFailed"
	ConditionKubeletFailed                   = "KubeletConfig failure"
	ConditionFailedGettingKubeletStatus      = "GettingKubeletStatusFailed"
	ConditionReasonTunedDegraded             = "TunedProfileDegraded"
	ConditionFailedGettingTunedProfileStatus = "GettingTunedStatusFailed"
)

type Writer interface {
	// Update updates the status reported by the controller
	Update(ctx context.Context, object client.Object, conditions []conditionsv1.Condition) error
	// UpdateOwnedConditions updates the conditions of the components owned by the controller
	UpdateOwnedConditions(ctx context.Context, object client.Object) error
}

func GetAvailableConditions(message string) []conditionsv1.Condition {
	now := time.Now()
	return []conditionsv1.Condition{
		{
			Type:               conditionsv1.ConditionAvailable,
			Status:             corev1.ConditionTrue,
			Message:            message,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionUpgradeable,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionProgressing,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionDegraded,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
	}
}

func GetDegradedConditions(reason string, message string) []conditionsv1.Condition {
	now := time.Now()
	return []conditionsv1.Condition{
		{
			Type:               conditionsv1.ConditionAvailable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionUpgradeable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionProgressing,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionDegraded,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: now},
			LastHeartbeatTime:  metav1.Time{Time: now},
			Reason:             reason,
			Message:            message,
		},
	}
}

func GetProgressingConditions(reason string, message string) []conditionsv1.Condition {
	now := time.Now()

	return []conditionsv1.Condition{
		{
			Type:               conditionsv1.ConditionAvailable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionUpgradeable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
		},
		{
			Type:               conditionsv1.ConditionProgressing,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: now},
			Reason:             reason,
			Message:            message,
		},
		{
			Type:               conditionsv1.ConditionDegraded,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: now},
		},
	}
}

func GetMCPDegradedCondition(profileMCP *mcov1.MachineConfigPool) ([]conditionsv1.Condition, error) {
	message := bytes.Buffer{}
	for _, condition := range profileMCP.Status.Conditions {
		if (condition.Type == mcov1.MachineConfigPoolNodeDegraded || condition.Type == mcov1.MachineConfigPoolRenderDegraded) && condition.Status == corev1.ConditionTrue {
			if len(condition.Reason) > 0 {
				message.WriteString("Machine config pool " + profileMCP.Name + " Degraded Reason: " + condition.Reason + ".\n")
			}
			if len(condition.Message) > 0 {
				message.WriteString("Machine config pool " + profileMCP.Name + " Degraded Message: " + condition.Message + ".\n")
			}
		}
	}

	messageString := message.String()
	if len(messageString) == 0 {
		return nil, nil
	}

	return GetDegradedConditions(ConditionReasonMCPDegraded, messageString), nil
}

func GetKubeletConditionsByProfile(ctx context.Context, client client.Client, profileName string) ([]conditionsv1.Condition, error) {
	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	kc, err := resources.GetKubeletConfig(ctx, client, name)

	// do not drop an error when kubelet config does not exist
	if errors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	latestCondition := getLatestKubeletConfigCondition(kc.Status.Conditions)
	if latestCondition == nil {
		return nil, nil
	}

	if latestCondition.Type != mcov1.KubeletConfigFailure {
		return nil, nil
	}

	return GetDegradedConditions(ConditionKubeletFailed, latestCondition.Message), nil
}

func GetTunedConditionsByProfile(ctx context.Context, cli client.Client, profile *performancev2.PerformanceProfile) ([]conditionsv1.Condition, error) {
	tunedProfileList := &tunedv1.ProfileList{}
	if err := cli.List(ctx, tunedProfileList); err != nil {
		klog.Errorf("Cannot list Tuned Profiles to match with profile %q: %v", profile.Name, err)
		return nil, err
	}

	selector := labels.SelectorFromSet(profile.Spec.NodeSelector)
	nodes := &corev1.NodeList{}
	if err := cli.List(ctx, nodes, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	// remove Tuned profiles that are not associate with this performance profile
	// Tuned profile's name and node's name should be equal
	filtered := removeUnMatchedTunedProfiles(nodes.Items, tunedProfileList.Items)

	messageString := GetTunedProfilesMessage(filtered)
	if len(messageString) == 0 {
		return nil, nil
	}

	return GetDegradedConditions(ConditionReasonTunedDegraded, messageString), nil
}

func GetTunedProfilesMessage(profiles []tunedv1.Profile) string {
	message := bytes.Buffer{}
	for _, tunedProfile := range profiles {
		isDegraded := false
		isApplied := true
		var tunedDegradedCondition *tunedv1.ProfileStatusCondition

		for i := 0; i < len(tunedProfile.Status.Conditions); i++ {
			condition := &tunedProfile.Status.Conditions[i]
			if (condition.Type == tunedv1.TunedDegraded) && condition.Status == corev1.ConditionTrue {
				isDegraded = true
				tunedDegradedCondition = condition
			}

			if (condition.Type == tunedv1.TunedProfileApplied) && condition.Status == corev1.ConditionFalse {
				isApplied = false
			}
		}
		// We need both conditions to exist,
		// since there is a scenario where both Degraded & Applied conditions are true
		if isDegraded && !isApplied {
			if len(tunedDegradedCondition.Reason) > 0 {
				message.WriteString("Tuned " + tunedProfile.GetName() + " Degraded Reason: " + tunedDegradedCondition.Reason + ".\n")
			}
			if len(tunedDegradedCondition.Message) > 0 {
				message.WriteString("Tuned " + tunedProfile.GetName() + " Degraded Message: " + tunedDegradedCondition.Message + ".\n")
			}
		}
	}
	return message.String()
}

func getLatestKubeletConfigCondition(conditions []mcov1.KubeletConfigCondition) *mcov1.KubeletConfigCondition {
	var latestCondition *mcov1.KubeletConfigCondition
	for i := 0; i < len(conditions); i++ {
		if latestCondition == nil || latestCondition.LastTransitionTime.Before(&conditions[i].LastTransitionTime) {
			latestCondition = &conditions[i]
		}
	}
	return latestCondition
}

func GetLatestContainerRuntimeConfigCondition(conditions []mcov1.ContainerRuntimeConfigCondition) *mcov1.ContainerRuntimeConfigCondition {
	var latestCondition *mcov1.ContainerRuntimeConfigCondition
	for i := 0; i < len(conditions); i++ {
		if latestCondition == nil || latestCondition.LastTransitionTime.Before(&conditions[i].LastTransitionTime) {
			latestCondition = &conditions[i]
		}
	}
	return latestCondition
}

func removeUnMatchedTunedProfiles(nodes []corev1.Node, profiles []tunedv1.Profile) []tunedv1.Profile {
	filteredProfiles := make([]tunedv1.Profile, 0)
	for _, profile := range profiles {
		for _, node := range nodes {
			if profile.Name == node.Name {
				filteredProfiles = append(filteredProfiles, profile)
				break
			}
		}
	}
	return filteredProfiles
}

func CalculateUpdated(prevStatus *performancev2.PerformanceProfileStatus, profileName, npName string, conditions []conditionsv1.Condition) *performancev2.PerformanceProfileStatus {
	statusCopy := prevStatus.DeepCopy()

	if conditions != nil {
		statusCopy.Conditions = conditions
	}

	// check if we need to update the status
	modified := false

	// since we always set the same four conditions, we don't need to check if we need to remove old conditions
	for _, newCondition := range statusCopy.Conditions {
		oldCondition := conditionsv1.FindStatusCondition(prevStatus.Conditions, newCondition.Type)
		if oldCondition == nil {
			modified = true
			break
		}

		// ignore timestamps to avoid infinite reconcile loops
		if oldCondition.Status != newCondition.Status ||
			oldCondition.Reason != newCondition.Reason ||
			oldCondition.Message != newCondition.Message {
			modified = true
			break
		}
	}

	if statusCopy.Tuned == nil {
		tunedNamespacedName := types.NamespacedName{
			Name:      components.GetComponentName(profileName, components.ProfileNamePerformance),
			Namespace: components.NamespaceNodeTuningOperator,
		}
		if npName != "" {
			tunedNamespacedName.Name = nto.MakeTunedUniqueName(tunedNamespacedName.Name, npName)
		}
		tunedStatus := tunedNamespacedName.String()
		statusCopy.Tuned = &tunedStatus
		modified = true
	}

	if statusCopy.RuntimeClass == nil {
		runtimeClassName := components.GetComponentName(profileName, components.ComponentNamePrefix)
		statusCopy.RuntimeClass = &runtimeClassName
		modified = true
	}

	if !modified {
		return nil
	}
	return statusCopy
}
