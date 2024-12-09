package tuned

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

// SetStatusCondition returns the result of setting the specified condition in
// the given slice of conditions.
func SetStatusCondition(oldConditions []tunedv1.StatusCondition, condition *tunedv1.StatusCondition) []tunedv1.StatusCondition {
	condition.LastTransitionTime = metav1.Now()

	newConditions := []tunedv1.StatusCondition{}

	found := false
	for _, c := range oldConditions {
		if condition.Type == c.Type {
			if condition.Status == c.Status &&
				condition.Reason == c.Reason &&
				condition.Message == c.Message {
				return oldConditions
			}

			found = true
			newConditions = append(newConditions, *condition)
		} else {
			newConditions = append(newConditions, c)
		}
	}
	if !found {
		newConditions = append(newConditions, *condition)
	}

	return newConditions
}

// ConditionsEqual returns true if and only if the provided slices of conditions
// (ignoring LastTransitionTime) are equal.
func ConditionsEqual(oldConditions, newConditions []tunedv1.StatusCondition) bool {
	if len(newConditions) != len(oldConditions) {
		return false
	}

	for _, conditionA := range oldConditions {
		foundMatchingCondition := false

		for _, conditionB := range newConditions {
			// Compare every field except LastTransitionTime.
			if conditionA.Type == conditionB.Type &&
				conditionA.Status == conditionB.Status &&
				conditionA.Reason == conditionB.Reason &&
				conditionA.Message == conditionB.Message {
				foundMatchingCondition = true
				break
			}
		}

		if !foundMatchingCondition {
			return false
		}
	}

	return true
}

// InitializeStatusConditions returns a slice of tunedv1.StatusCondition
// initialized to an unknown state.
func InitializeStatusConditions() []tunedv1.StatusCondition {
	now := metav1.Now()
	return []tunedv1.StatusCondition{
		{
			Type:               tunedv1.TunedProfileApplied,
			Status:             corev1.ConditionUnknown,
			LastTransitionTime: now,
		},
		{
			Type:               tunedv1.TunedDegraded,
			Status:             corev1.ConditionUnknown,
			LastTransitionTime: now,
		},
	}
}

// computeStatusConditions takes the set of Bits 'status', old conditions
// 'conditions', an optional 'message' to put in the relevant condition field,
// and returns an updated slice of tunedv1.StatusCondition.
// 'status' contains all the information necessary for creating a new slice of
// conditions apart from LastTransitionTime, which is set based on checking the
// old conditions.
func computeStatusConditions(status Bits, message string, conditions []tunedv1.StatusCondition) []tunedv1.StatusCondition {
	if (status & scUnknown) != 0 {
		return InitializeStatusConditions()
	}

	tunedProfileAppliedCondition := tunedv1.StatusCondition{
		Type: tunedv1.TunedProfileApplied,
	}
	tunedDegradedCondition := tunedv1.StatusCondition{
		Type: tunedv1.TunedDegraded,
	}

	deferredMessage := ""
	if len(message) > 0 {
		deferredMessage = ": " + message
	}

	if (status & scDeferred) != 0 {
		tunedProfileAppliedCondition.Status = corev1.ConditionFalse
		tunedProfileAppliedCondition.Reason = "Deferred"
		tunedProfileAppliedCondition.Message = "The TuneD daemon profile is waiting for the next node restart" + deferredMessage
	} else if (status & scApplied) != 0 {
		tunedProfileAppliedCondition.Status = corev1.ConditionTrue
		tunedProfileAppliedCondition.Reason = "AsExpected"
		tunedProfileAppliedCondition.Message = "TuneD profile applied."
	} else {
		tunedProfileAppliedCondition.Status = corev1.ConditionFalse
		tunedProfileAppliedCondition.Reason = "Failed"
		tunedProfileAppliedCondition.Message = "The TuneD daemon profile not yet applied, or application failed."
	}

	if (status & scError) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionTrue
		tunedDegradedCondition.Reason = "TunedError"
		tunedDegradedCondition.Message = "TuneD daemon issued one or more error message(s) during profile application. TuneD stderr: " + message
	} else if (status & scDeferred) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionTrue
		tunedDegradedCondition.Reason = "TunedDeferredUpdate"
		tunedDegradedCondition.Message = "Profile will be applied at the next node restart" + deferredMessage
	} else if (status & scSysctlOverride) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionTrue // treat overrides as regular errors; users should use "reapply_sysctl: true" or remove conflicting sysctls
		tunedDegradedCondition.Reason = "TunedSysctlOverride"
		tunedDegradedCondition.Message = "TuneD daemon issued one or more sysctl override message(s) during profile application. Use reapply_sysctl=true or remove conflicting sysctl " + message
	} else if (status & scWarn) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionFalse // consider warnings from TuneD as non-fatal
		tunedDegradedCondition.Reason = "TunedWarning"
		tunedDegradedCondition.Message = "No error messages observed by applying the TuneD daemon profile, only warning(s). TuneD stderr: " + message
	} else {
		tunedDegradedCondition.Status = corev1.ConditionFalse
		tunedDegradedCondition.Reason = "AsExpected"
		tunedDegradedCondition.Message = "No warning or error messages observed applying the TuneD daemon profile."
	}

	conditions = SetStatusCondition(conditions, &tunedProfileAppliedCondition)
	conditions = SetStatusCondition(conditions, &tunedDegradedCondition)

	return conditions
}
