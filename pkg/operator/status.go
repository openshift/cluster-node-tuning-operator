package operator

import (
	"context"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/clusteroperator"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
)

const (
	conditionFailedToSyncTunedDaemonSet  = "FailedToSyncTunedDaemonSet"
	conditionFailedToSyncDefaultTuned    = "FailedToSyncDefaultTuned"
	conditionFailedToGetTunedDaemonSet   = "FailedToGetTunedDaemonSet"
	conditionTunedDaemonSetProgressing   = "TunedDaemonSetProgressing"
	conditionSucceededToDeployComponents = "SucceededToDeployComponents"
)

const maxTunedUnavailable = 7200

//TODO - IMPORTANT: check if we still need to use more specific API calls that were delegated using clusteroperator.go

// syncOperatorStatus computes the operator's current status and therefrom
// creates or updates the ClusterOperator resource for the operator.
func (r *Reconciler) syncOperatorStatus() error {
	klog.V(2).Infof("syncOperatorStatus()")

	co, err := r.getOrCreateOperatorStatus()
	if err != nil {
		return err
	}
	co = co.DeepCopy() // Never modify objects in cache

	oldConditions := co.Status.Conditions
	co.Status.Conditions, err = r.computeStatusConditions(oldConditions)
	if err != nil {
		return err
	}
	// Every operator must report its version from the payload.
	// If the operator is reporting available, it resets the release version to the present value.
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		if conditionTrue(co.Status.Conditions, configv1.OperatorAvailable) {
			operatorv1helpers.SetOperandVersion(&co.Status.Versions, configv1.OperandVersion{Name: "operator", Version: releaseVersion})
		}
	}
	co.Status.RelatedObjects = getRelatedObjects()

	if clusteroperator.ConditionsEqual(oldConditions, co.Status.Conditions) {
		klog.V(2).Infof("syncOperatorStatus(): ConditionsEqual")
		return nil
	}

	//TODO - check if refactor is needed for metrics
	metrics.Degraded(conditionTrue(co.Status.Conditions, configv1.OperatorDegraded))
	if err := r.Client.Status().Update(context.TODO(), co); err != nil {
		klog.Errorf("unable to update ClusterOperator: %v", err)
		return err
	}
	return nil
}

// getOrCreateOperatorStatus get the currect ClusterOperator object or creates a new one.
// The method always returns a DeepCopy() of the object so the return value is safe to use
// for Update()s.
func (r *Reconciler) getOrCreateOperatorStatus() (*configv1.ClusterOperator, error) {
	co := &configv1.ClusterOperator{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      tunedv1.TunedClusterOperatorResourceName,
	}
	err := r.Client.Get(context.TODO(), key, co)
	if err != nil {
		if errors.IsNotFound(err) {
			// Cluster operator not found, create it.
			co = &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: tunedv1.TunedClusterOperatorResourceName}}
			if err := r.Client.Create(context.TODO(), co); err != nil {
				return nil, fmt.Errorf("failed to create clusteroperator %s: %v", co.Name, err)
			}
			return co, nil
		} else {
			return nil, err
		}
	}
	return co, nil
}

// profileApplied returns true if Tuned Profile 'profile' has been applied.
func profileApplied(profile *tunedv1.Profile) bool {
	if profile == nil || profile.Spec.Config.TunedProfile != profile.Status.TunedProfile {
		return false
	}

	for _, sc := range profile.Status.Conditions {
		if sc.Type == tunedv1.TunedProfileApplied && sc.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// profileDegraded returns true if Tuned Profile 'profile' has not been applied
// or applied with errors (Degraded).
func profileDegraded(profile *tunedv1.Profile) bool {
	if profile == nil {
		return false
	}

	for _, sc := range profile.Status.Conditions {
		if (sc.Type == tunedv1.TunedProfileApplied && sc.Status == corev1.ConditionFalse) ||
			(sc.Type == tunedv1.TunedDegraded && sc.Status == corev1.ConditionTrue) {
			return true
		}
	}

	return false
}

// anyProfileDegraded returns true if any of the Tuned Profiles in the slice
// 'profileList' has not been applied or applied with errors (Degraded).
func anyProfileDegraded(profileList *tunedv1.ProfileList) bool {
	if profileList.Items == nil {
		return false
	}
	for _, profile := range profileList.Items {
		if profileDegraded(&profile) {
			return true
		}
	}

	return false
}

// computeStatusConditions computes the operator's current state.
func (r *Reconciler) computeStatusConditions(conditions []configv1.ClusterOperatorStatusCondition) ([]configv1.ClusterOperatorStatusCondition, error) {
	const (
		// maximum number of seconds for the operator to be Unavailable with a unique
		// Reason/Message before setting the Degraded ClusterOperator condition
		maxTunedUnavailable = 7200
	)
	dsMf := ntomf.TunedDaemonSet()

	availableCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorAvailable,
	}
	progressingCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorProgressing,
	}
	degradedCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorDegraded,
	}

	upgradeableCondition := configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorUpgradeable,
		Status: configv1.ConditionTrue,
	}

	copyAvailableCondition := func() {
		progressingCondition.Status = availableCondition.Status
		progressingCondition.Reason = availableCondition.Reason
		progressingCondition.Message = availableCondition.Message

		degradedCondition.Status = availableCondition.Status
		degradedCondition.Reason = availableCondition.Reason
		degradedCondition.Message = availableCondition.Message
	}

	ds := &appsv1.DaemonSet{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      dsMf.Name,
	}
	err := r.Client.Get(context.TODO(), key, ds)
	if err != nil {
		// There was a problem fetching Tuned daemonset
		if errors.IsNotFound(err) {
			// Tuned daemonset has not been created yet
			if len(conditions) == 0 {
				// This looks like a fresh install => initialize
				klog.V(3).Infof("no ClusterOperator conditions set, initializing them.")
				availableCondition.Status = configv1.ConditionFalse
				availableCondition.Reason = "TunedUnavailable"
				availableCondition.Message = fmt.Sprintf("DaemonSet %q unavailable", dsMf.Name)

				progressingCondition.Status = configv1.ConditionTrue
				progressingCondition.Reason = "Reconciling"
				progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))

				degradedCondition.Status = configv1.ConditionFalse
				degradedCondition.Reason = progressingCondition.Reason
				degradedCondition.Message = progressingCondition.Message
			} else {
				// This should not happen unless there was a manual intervention.
				// Preserve the previously known conditions and requeue.
				klog.Errorf("unable to calculate Operator status conditions, preserving the old ones: %v", err)
				return conditions, err
			}
		} else {
			klog.Errorf("setting all ClusterOperator conditions to Unknown: %v", err)
			availableCondition.Status = configv1.ConditionUnknown
			availableCondition.Reason = "Unknown"
			availableCondition.Message = fmt.Sprintf("Unable to fetch DaemonSet %q: %v", dsMf.Name, err)

			copyAvailableCondition()
		}
	} else {
		if ds.Status.NumberAvailable > 0 {
			// The operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionTrue
			availableCondition.Reason = "AsExpected"
			if ds.Status.UpdatedNumberScheduled > 0 {
				// At least one operand instance runs RELEASE_VERSION, report it
				klog.V(3).Infof("%d operands run release version %q", ds.Status.UpdatedNumberScheduled, os.Getenv("RELEASE_VERSION"))
				availableCondition.Message = fmt.Sprintf("Cluster has deployed %q", os.Getenv("RELEASE_VERSION"))
			}
		} else {
			// No operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionFalse
			availableCondition.Reason = "TunedUnavailable"
			availableCondition.Message = fmt.Sprintf("DaemonSet %q has no available Pod(s)", dsMf.Name)
			klog.V(3).Infof("syncOperatorStatus(): %s", availableCondition.Message)
		}

		// The operator is actively making changes to the operand (is Progressing) when:
		// the total number of Nodes that should be running the daemon Pod
		// (including Nodes correctly running the daemon Pod) != the total number of
		// Nodes that are running updated daemon Pod.
		if ds.Status.DesiredNumberScheduled != ds.Status.UpdatedNumberScheduled ||
			ds.Status.DesiredNumberScheduled == 0 {
			klog.V(3).Infof("setting Progressing condition to true")
			progressingCondition.Status = configv1.ConditionTrue
			progressingCondition.Reason = "Reconciling"
			progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))
		} else {
			progressingCondition.Status = configv1.ConditionFalse
			progressingCondition.Reason = "AsExpected"
			progressingCondition.Message = fmt.Sprintf("Cluster version is %q", os.Getenv("RELEASE_VERSION"))
		}

		degradedCondition.Status = configv1.ConditionFalse
		degradedCondition.Reason = "AsExpected"
		degradedCondition.Message = fmt.Sprintf("DaemonSet %q available", dsMf.Name)
	}

	// Check the Profile status Degraded conditions.
	var reportDegraded bool

	profileList := &tunedv1.ProfileList{}
	err = r.Client.List(context.TODO(), profileList)
	if err != nil {
		// Failed to list Tuned Profiles.
		availableCondition.Status = configv1.ConditionUnknown
		availableCondition.Reason = "Unknown"
		availableCondition.Message = fmt.Sprintf("Unable to fetch Tuned Profiles: %v", err)

		copyAvailableCondition()
	}
	reportDegraded = anyProfileDegraded(profileList)

	if reportDegraded {
		klog.Infof("at least one Profile application failing")
		availableCondition.Reason = "ProfileDegraded"
		availableCondition.Message = fmt.Sprintf("At least one Profile application failing")
	}

	// If the operator is not available for an extensive period of time, set the Degraded operator status.
	conditions = clusteroperator.SetStatusCondition(conditions, &availableCondition)
	now := metav1.Now().Unix()
	for _, condition := range conditions {
		if condition.Type == configv1.OperatorAvailable &&
			condition.Status == configv1.ConditionFalse &&
			now-condition.LastTransitionTime.Unix() > maxTunedUnavailable {
			klog.Infof("operator unavailable for longer than %d seconds, setting Degraded status.", maxTunedUnavailable)
			degradedCondition.Status = configv1.ConditionTrue
			degradedCondition.Reason = "TunedUnavailable"
			degradedCondition.Message = fmt.Sprintf("DaemonSet %q unavailable for more than %d seconds", dsMf.Name, maxTunedUnavailable)
		}
	}

	conditions = clusteroperator.SetStatusCondition(conditions, &availableCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &progressingCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &degradedCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &upgradeableCondition)

	klog.V(3).Infof("operator status conditions: %v", conditions)

	return conditions, nil
}

func getRelatedObjects() []configv1.ObjectReference {
	tunedMf := ntomf.TunedCustomResource()
	dsMf := ntomf.TunedDaemonSet()

	return []configv1.ObjectReference{
		// The `resource` property of `relatedObjects` stanza should be the lowercase, plural value like `daemonsets`.
		// See BZ1851214
		{Group: "", Resource: "namespaces", Name: tunedMf.Namespace},
		{Group: "tuned.openshift.io", Resource: "profiles", Name: "", Namespace: tunedMf.Namespace},
		{Group: "tuned.openshift.io", Resource: "tuneds", Name: "", Namespace: tunedMf.Namespace},
		{Group: "apps", Resource: "daemonsets", Name: dsMf.Name, Namespace: dsMf.Namespace},
	}
}

func conditionTrue(conditions []configv1.ClusterOperatorStatusCondition, condType configv1.ClusterStatusConditionType) bool {
	for _, condition := range conditions {
		if condition.Type == condType {
			return condition.Status == configv1.ConditionTrue
		}
	}

	return false
}

// updateConditions updates the specific condition to have status True and all others conditions to have status False
// the only exception is Available condition because we should update the Upgradeable condition to True as well
func updateConditions(
	conditions []configv1.ClusterOperatorStatusCondition,
	conditionType configv1.ClusterStatusConditionType,
	reason string,
	message string,
) {
	now := metav1.Now()
	for i := range conditions {
		c := &conditions[i]
		if c.Type == conditionType {
			conditions[i] = configv1.ClusterOperatorStatusCondition{
				Type:               c.Type,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			}
			continue
		}

		// Once we update the available condition we also should update the cluster operator to be upgradeable except the unmanaged state.
		if conditionType == configv1.OperatorAvailable && c.Type == configv1.OperatorUpgradeable && reason != string(operatorv1.Unmanaged) {
			conditions[i] = configv1.ClusterOperatorStatusCondition{
				Type:               c.Type,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: now,
			}
			continue
		}

		// For all other conditions the status should be false.
		if !isConditionStatusUpdatedToFalse(c) {
			conditions[i] = configv1.ClusterOperatorStatusCondition{
				Type:               c.Type,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: now,
			}
		}
	}
}

// isConditionStatusUpdatedToFalse verifies that the specified condition has status False and
// empty message and reason fields
func isConditionStatusUpdatedToFalse(c *configv1.ClusterOperatorStatusCondition) bool {
	return c.Status == configv1.ConditionFalse && c.Message == "" && c.Reason == ""
}

func (r *Reconciler) updateClusterOperatorStatus(
	ctx context.Context,
	co *configv1.ClusterOperator,
	conditionType configv1.ClusterStatusConditionType,
	reason string,
	message string,
) error {
	coNew := co.DeepCopy()
	// create default conditions
	if len(coNew.Status.Conditions) == 0 {
		coNew.Status.Conditions = getDefaultConditions()
	}
	updateConditions(coNew.Status.Conditions, conditionType, reason, message)
	coNew.Status.RelatedObjects = getRelatedObjects()

	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		if conditionTrue(coNew.Status.Conditions, configv1.OperatorAvailable) {
			operatorv1helpers.SetOperandVersion(
				&coNew.Status.Versions,
				configv1.OperandVersion{Name: "operator", Version: releaseVersion},
			)
		}
	}

	// nothing to update
	if equality.Semantic.DeepEqual(co.Status, coNew.Status) {
		return nil
	}

	return r.Client.Status().Update(ctx, coNew)
}

func getDefaultConditions() []configv1.ClusterOperatorStatusCondition {
	return []configv1.ClusterOperatorStatusCondition{
		{
			Type:   configv1.OperatorAvailable,
			Status: configv1.ConditionTrue,
		},
		{
			Type:   configv1.OperatorDegraded,
			Status: configv1.ConditionTrue,
		},
		{
			Type:   configv1.OperatorProgressing,
			Status: configv1.ConditionTrue,
		},
		{
			Type:   configv1.OperatorUpgradeable,
			Status: configv1.ConditionTrue,
		},
	}
}
