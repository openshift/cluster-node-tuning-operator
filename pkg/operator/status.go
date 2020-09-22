package operator

import (
	"context"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/clusteroperator"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
)

// syncOperatorStatus computes the operator's current status and therefrom
// creates or updates the ClusterOperator resource for the operator.
func (c *Controller) syncOperatorStatus(tuned *tunedv1.Tuned) error {
	klog.V(2).Infof("syncOperatorStatus()")

	co, err := c.getOrCreateOperatorStatus()
	if err != nil {
		return err
	}
	co = co.DeepCopy() // Never modify objects in cache

	tunedMf := ntomf.TunedCustomResource()
	dsMf := ntomf.TunedDaemonSet()

	oldConditions := co.Status.Conditions
	co.Status.Conditions, err = c.computeStatusConditions(tuned, oldConditions, dsMf.Name)
	if err != nil {
		return err
	}
	// every operator must report its version from the payload
	// if the operator is reporting available, it resets the release version to the present value
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		if conditionTrue(co.Status.Conditions, configv1.OperatorAvailable) {
			operatorv1helpers.SetOperandVersion(&co.Status.Versions, configv1.OperandVersion{Name: "operator", Version: releaseVersion})
		}
	}
	co.Status.RelatedObjects = []configv1.ObjectReference{
		// The `resource` property of `relatedObjects` stanza should be the lowercase, plural value like `daemonsets`.
		// See BZ1851214
		{Group: "", Resource: "namespaces", Name: tunedMf.Namespace},
		{Group: "tuned.openshift.io", Resource: "tuneds", Name: tunedMf.Name, Namespace: tunedMf.Namespace},
		{Group: "apps", Resource: "daemonsets", Name: dsMf.Name, Namespace: dsMf.Namespace},
	}

	if clusteroperator.ConditionsEqual(oldConditions, co.Status.Conditions) {
		klog.V(2).Infof("syncOperatorStatus(): ConditionsEqual")
		return nil
	}

	metrics.Degraded(conditionTrue(co.Status.Conditions, configv1.OperatorDegraded))
	_, err = c.clients.Config.ClusterOperators().UpdateStatus(context.TODO(), co, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("unable to update ClusterOperator: %v", err)
		return err
	}

	return nil
}

// getOrCreateOperatorStatus get the currect ClusterOperator object or creates a new one.
// The method always returns a DeepCopy() of the object so the return value is safe to use
// for Update()s.
func (c *Controller) getOrCreateOperatorStatus() (*configv1.ClusterOperator, error) {
	co, err := c.listers.ClusterOperators.Get(tunedv1.TunedClusterOperatorResourceName)
	if err != nil {
		if errors.IsNotFound(err) {
			// Cluster operator not found, create it
			co = &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: tunedv1.TunedClusterOperatorResourceName}}
			initializeClusterOperator(co)
			co, err = c.clients.Config.ClusterOperators().Create(context.TODO(), co, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to create clusteroperator %s: %v", co.Name, err)
			}
			return co, nil
		} else {
			return nil, err
		}
	}
	return co, nil
}

// computeStatusConditions computes the operator's current state.
func (c *Controller) computeStatusConditions(tuned *tunedv1.Tuned, conditions []configv1.ClusterOperatorStatusCondition,
	dsName string) ([]configv1.ClusterOperatorStatusCondition, error) {
	const (
		// maximum number of seconds for the operator to be Unavailable with a unique
		// Reason/Message before setting the Degraded ClusterOperator condition
		maxTunedUnavailable = 7200
	)
	availableCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorAvailable,
	}
	progressingCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorProgressing,
	}
	degradedCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorDegraded,
	}

	switch tuned.Spec.ManagementState {
	case operatorv1.Unmanaged:
		availableCondition.Reason = "Unmanaged"
		availableCondition.Message = "The operator configuration is set to unmanaged mode"

	case operatorv1.Removed:
		availableCondition.Reason = "Removed"
		availableCondition.Message = "The operator is removed"

	default:
		ds, err := c.listers.DaemonSets.Get(dsName)
		if err != nil {
			// There was a problem fetching Tuned daemonset
			if errors.IsNotFound(err) {
				// Tuned daemonset has not been created yet
				if len(conditions) == 0 {
					// This looks like a fresh install => initialize
					klog.V(3).Infof("no ClusterOperator conditions set, initializing them.")
					availableCondition.Status = configv1.ConditionFalse
					availableCondition.Reason = "TunedUnavailable"
					availableCondition.Message = fmt.Sprintf("DaemonSet %q unavailable", dsName)

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
				availableCondition.Message = fmt.Sprintf("Unable to fetch DaemonSet %q: %v", dsName, err)

				progressingCondition.Status = availableCondition.Status
				progressingCondition.Reason = availableCondition.Reason
				progressingCondition.Message = availableCondition.Message

				degradedCondition.Status = availableCondition.Status
				degradedCondition.Reason = availableCondition.Reason
				degradedCondition.Message = availableCondition.Message
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
				availableCondition.Message = fmt.Sprintf("DaemonSet %q has no available Pod(s)", dsName)
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
			degradedCondition.Message = fmt.Sprintf("DaemonSet %q available", dsName)
		}

		// If the operator is not available for an extensive period of time, set the Degraded operator status
		conditions = clusteroperator.SetStatusCondition(conditions, &availableCondition)
		now := metav1.Now().Unix()
		for _, condition := range conditions {
			if condition.Type == configv1.OperatorAvailable &&
				condition.Status == configv1.ConditionFalse &&
				now-condition.LastTransitionTime.Unix() > maxTunedUnavailable {
				klog.V(3).Infof("operator unavailable for longer than %d seconds, setting Degraded status.", maxTunedUnavailable)
				degradedCondition.Status = configv1.ConditionTrue
				degradedCondition.Reason = "TunedUnavailable"
				degradedCondition.Message = fmt.Sprintf("DaemonSet %q unavailable for more than %d seconds", dsName, maxTunedUnavailable)
			}
		}
	}

	switch tuned.Spec.ManagementState {
	case operatorv1.Removed, operatorv1.Unmanaged:
		availableCondition.Status = configv1.ConditionTrue

		progressingCondition.Status = configv1.ConditionFalse
		progressingCondition.Reason = availableCondition.Reason
		progressingCondition.Message = availableCondition.Message

		degradedCondition.Status = configv1.ConditionFalse
		degradedCondition.Reason = availableCondition.Reason
		degradedCondition.Message = availableCondition.Message

	default:
	}

	conditions = clusteroperator.SetStatusCondition(conditions, &availableCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &progressingCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &degradedCondition)

	klog.V(3).Infof("operator status conditions: %v", conditions)

	return conditions, nil
}

// Populate versions and conditions in cluster operator status as CVO expects these fields.
func initializeClusterOperator(co *configv1.ClusterOperator) {
	co.Status.Versions = []configv1.OperandVersion{
		{
			Name:    "operator",
			Version: "unknown",
		},
	}
	co.Status.Conditions = []configv1.ClusterOperatorStatusCondition{
		{
			Type:   configv1.OperatorAvailable,
			Status: configv1.ConditionUnknown,
		},
		{
			Type:   configv1.OperatorProgressing,
			Status: configv1.ConditionUnknown,
		},
		{
			Type:   configv1.OperatorDegraded,
			Status: configv1.ConditionUnknown,
		},
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
