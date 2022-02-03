package controllers

import (
	"bytes"
	"context"
	"time"

	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	conditionFailedToFindMachineConfigPool   = "GettingMachineConfigPoolFailed"
	conditionBadMachineConfigLabels          = "BadMachineConfigLabels"
	conditionReasonComponentsCreationFailed  = "ComponentCreationFailed"
	conditionReasonMCPDegraded               = "MCPDegraded"
	conditionFailedGettingMCPStatus          = "GettingMCPStatusFailed"
	conditionKubeletFailed                   = "KubeletConfig failure"
	conditionFailedGettingKubeletStatus      = "GettingKubeletStatusFailed"
	conditionReasonTunedDegraded             = "TunedProfileDegraded"
	conditionFailedGettingTunedProfileStatus = "GettingTunedStatusFailed"
)

func (r *PerformanceProfileReconciler) updateStatus(profile *performancev2.PerformanceProfile, conditions []conditionsv1.Condition) error {
	profileCopy := profile.DeepCopy()

	if conditions != nil {
		profileCopy.Status.Conditions = conditions
	}

	// check if we need to update the status
	modified := false

	// since we always set the same four conditions, we don't need to check if we need to remove old conditions
	for _, newCondition := range profileCopy.Status.Conditions {
		oldCondition := conditionsv1.FindStatusCondition(profile.Status.Conditions, newCondition.Type)
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

	if profileCopy.Status.Tuned == nil {
		tunedNamespacedname := types.NamespacedName{
			Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
			Namespace: components.NamespaceNodeTuningOperator,
		}
		tunedStatus := tunedNamespacedname.String()
		profileCopy.Status.Tuned = &tunedStatus
		modified = true
	}

	if profileCopy.Status.RuntimeClass == nil {
		runtimeClassName := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
		profileCopy.Status.RuntimeClass = &runtimeClassName
		modified = true
	}

	if !modified {
		return nil
	}

	klog.Infof("Updating the performance profile %q status", profile.Name)
	return r.Status().Update(context.TODO(), profileCopy)
}

func (r *PerformanceProfileReconciler) getAvailableConditions() []conditionsv1.Condition {
	now := time.Now()
	return []conditionsv1.Condition{
		{
			Type:               conditionsv1.ConditionAvailable,
			Status:             corev1.ConditionTrue,
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

func (r *PerformanceProfileReconciler) getDegradedConditions(reason string, message string) []conditionsv1.Condition {
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

func (r *PerformanceProfileReconciler) getProgressingConditions(reason string, message string) []conditionsv1.Condition {
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

func (r *PerformanceProfileReconciler) getMCPDegradedCondition(profileMCP *mcov1.MachineConfigPool) ([]conditionsv1.Condition, error) {
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

	return r.getDegradedConditions(conditionReasonMCPDegraded, messageString), nil
}

func (r *PerformanceProfileReconciler) getKubeletConditionsByProfile(profile *performancev2.PerformanceProfile) ([]conditionsv1.Condition, error) {
	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	kc, err := r.getKubeletConfig(name)

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

	return r.getDegradedConditions(conditionKubeletFailed, latestCondition.Message), nil
}

func (r *PerformanceProfileReconciler) getTunedConditionsByProfile(profile *performancev2.PerformanceProfile) ([]conditionsv1.Condition, error) {
	tunedProfileList := &tunedv1.ProfileList{}
	if err := r.List(context.TODO(), tunedProfileList); err != nil {
		klog.Errorf("Cannot list Tuned Profiles to match with profile %q : %v", profile.Name, err)
		return nil, err
	}

	selector := labels.SelectorFromSet(profile.Spec.NodeSelector)
	nodes := &corev1.NodeList{}
	if err := r.List(context.TODO(), nodes, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	// remove Tuned profiles that are not associate with this perfomance profile
	// Tuned profile's name and node's name should be equal
	filtered := removeUnMatchedTunedProfiles(nodes.Items, tunedProfileList.Items)
	message := bytes.Buffer{}
	for _, tunedProfile := range filtered {
		isDegraded := false
		isApplied := true
		var tunedDegradedCondition *tunedv1.ProfileStatusCondition

		for _, condition := range tunedProfile.Status.Conditions {
			if (condition.Type == tunedv1.TunedDegraded) && condition.Status == corev1.ConditionTrue {
				isDegraded = true
				tunedDegradedCondition = &condition
			}

			if (condition.Type == tunedv1.TunedProfileApplied) && condition.Status == corev1.ConditionFalse {
				isApplied = false
			}
		}
		// We need both condition to exists,
		// since there is a scenario where both Degraded & Applied condition are true
		if isDegraded == true && isApplied == false {
			if len(tunedDegradedCondition.Reason) > 0 {
				message.WriteString("Tuned " + tunedProfile.GetName() + " Degraded Reason: " + tunedDegradedCondition.Reason + ".\n")
			}
			if len(tunedDegradedCondition.Message) > 0 {
				message.WriteString("Tuned " + tunedProfile.GetName() + " Degraded Message: " + tunedDegradedCondition.Message + ".\n")
			}
		}
	}

	messageString := message.String()
	if len(messageString) == 0 {
		return nil, nil
	}

	return r.getDegradedConditions(conditionReasonTunedDegraded, messageString), nil
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
