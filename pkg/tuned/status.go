package tuned

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

// setStatusCondition returns the result of setting the specified condition in
// the given slice of conditions.
func setStatusCondition(oldConditions []tunedv1.ProfileStatusCondition, condition *tunedv1.ProfileStatusCondition) []tunedv1.ProfileStatusCondition {
	condition.LastTransitionTime = metav1.Now()

	newConditions := []tunedv1.ProfileStatusCondition{}

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

// conditionsEqual returns true if and only if the provided slices of conditions
// (ignoring LastTransitionTime) are equal.
func conditionsEqual(oldConditions, newConditions []tunedv1.ProfileStatusCondition) bool {
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

// computeStatusConditions takes the set of Bits 'status', old conditions
// 'conditions' and returns an updated slice of tunedv1.ProfileStatusCondition.
// 'status' contains all the information necessary for creating a new slice of
// conditions apart from LastTransitionTime, which is set based on checking the
// old conditions.
func computeStatusConditions(status Bits, conditions []tunedv1.ProfileStatusCondition) []tunedv1.ProfileStatusCondition {
	tunedProfileAppliedCondition := tunedv1.ProfileStatusCondition{
		Type: tunedv1.TunedProfileApplied,
	}
	tunedDegradedCondition := tunedv1.ProfileStatusCondition{
		Type: tunedv1.TunedDegraded,
	}

	if (status & scApplied) != 0 {
		tunedProfileAppliedCondition.Status = corev1.ConditionTrue
		tunedProfileAppliedCondition.Reason = "AsExpected"
		tunedProfileAppliedCondition.Message = "Tuned profile applied."
	} else {
		tunedProfileAppliedCondition.Status = corev1.ConditionFalse
		tunedProfileAppliedCondition.Reason = "Failed"
		tunedProfileAppliedCondition.Message = "The Tuned daemon profile application failed."
	}

	if (status & scError) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionTrue
		tunedDegradedCondition.Reason = "TunedError"
		tunedDegradedCondition.Message = "Tuned daemon issued one or more error message(s) during profile application."
	} else if (status & scWarn) != 0 {
		tunedDegradedCondition.Status = corev1.ConditionFalse // consider warnings from Tuned as non-fatal
		tunedDegradedCondition.Reason = "TunedWarning"
		tunedDegradedCondition.Message = "No error messages observed by applying the Tuned daemon profile, only warning(s)."
	} else {
		tunedDegradedCondition.Status = corev1.ConditionFalse
		tunedDegradedCondition.Reason = "AsExpected"
		tunedDegradedCondition.Message = "No warning or error messages observed applying the Tuned daemon profile."
	}

	conditions = setStatusCondition(conditions, &tunedProfileAppliedCondition)
	conditions = setStatusCondition(conditions, &tunedDegradedCondition)

	return conditions
}
