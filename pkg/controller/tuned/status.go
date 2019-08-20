package tuned

import (
	"context"
	"fmt"
	"os"

	"github.com/golang/glog"
	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util/clusteroperator"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// syncOperatorStatus computes the operator's current status and therefrom
// creates or updates the ClusterOperator resource for the operator.
func (r *ReconcileTuned) syncOperatorStatus() (bool, error) {
	var requeue bool

	glog.V(1).Infof("syncOperatorStatus()")

	coState, err := r.getOrCreateOperatorStatus()
	if err != nil {
		return false, err
	}

	tunedManifest, _ := r.manifestFactory.TunedCustomResource()
	saManifest, _ := r.manifestFactory.TunedServiceAccount()
	crManifest, _ := r.manifestFactory.TunedClusterRole()
	crbManifest, _ := r.manifestFactory.TunedClusterRoleBinding()
	cmManifestProfiles, _ := r.manifestFactory.TunedConfigMapProfiles([]tunedv1.Tuned{})
	cmManifestRecommend, _ := r.manifestFactory.TunedConfigMapRecommend([]tunedv1.Tuned{})

	dsManifest, _ := r.manifestFactory.TunedDaemonSet()
	daemonset := &appsv1.DaemonSet{}
	dsErr := r.client.Get(context.TODO(), types.NamespacedName{Namespace: dsManifest.Namespace, Name: dsManifest.Name}, daemonset)

	oldConditions := coState.Status.Conditions
	coState.Status.Conditions, requeue = computeStatusConditions(oldConditions, daemonset, dsErr)
	// every operator must report its version from the payload
	// if the operator is reporting available, it resets the release version to the present value
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		for _, condition := range coState.Status.Conditions {
			if condition.Type == configv1.OperatorAvailable && condition.Status == configv1.ConditionTrue {
				operatorv1helpers.SetOperandVersion(&coState.Status.Versions, configv1.OperandVersion{Name: "operator", Version: releaseVersion})
			}
		}
	}
	coState.Status.RelatedObjects = []configv1.ObjectReference{
		{Group: "", Resource: "namespaces", Name: tunedManifest.Namespace},
		{Group: "tuned.openshift.io", Resource: tunedManifest.Kind, Name: tunedManifest.Name, Namespace: tunedManifest.Namespace},
		{Group: "", Resource: saManifest.Kind, Name: saManifest.Name, Namespace: saManifest.Namespace},
		{Group: "rbac.authorization.k8s.io", Resource: crManifest.Kind, Name: crManifest.Name},
		{Group: "rbac.authorization.k8s.io", Resource: crbManifest.Kind, Name: crbManifest.Name, Namespace: crbManifest.Namespace},
		{Group: "", Resource: cmManifestProfiles.Kind, Name: cmManifestProfiles.Name, Namespace: cmManifestProfiles.Namespace},
		{Group: "", Resource: cmManifestRecommend.Kind, Name: cmManifestRecommend.Name, Namespace: cmManifestRecommend.Namespace},
		{Group: "apps", Resource: dsManifest.Kind, Name: dsManifest.Name, Namespace: dsManifest.Namespace},
	}

	if clusteroperator.ConditionsEqual(oldConditions, coState.Status.Conditions) {
		return requeue, nil
	}

	err = r.client.Status().Update(context.TODO(), coState)
	if err != nil {
		glog.Errorf("Unable to update coState")
		return requeue, err
	}

	return requeue, nil
}

func (r *ReconcileTuned) getOrCreateOperatorStatus() (*configv1.ClusterOperator, error) {
	co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: ntoconfig.OperatorName()}}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: co.Name}, co); err != nil {
		if errors.IsNotFound(err) {
			// Cluster operator not found, create it
			initializeClusterOperator(co)
			if err := r.client.Create(context.TODO(), co); err != nil {
				glog.Errorf("Failed to create clusteroperator %s: %v", co.Name, err)
				return nil, fmt.Errorf("Failed to create clusteroperator %s: %v", co.Name, err)
			}
			return co, nil
		} else {
			return nil, err
		}
	}
	return co, nil
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

// computeStatusConditions computes the operator's current state.
func computeStatusConditions(conditions []configv1.ClusterOperatorStatusCondition,
	daemonset *appsv1.DaemonSet,
	dsErr error) ([]configv1.ClusterOperatorStatusCondition, bool) {
	const (
		// maximum number of seconds for the operator to be Unavailable with a unique
		// Reason/Message before setting the Degraded ClusterOperator condition
		maxTunedUnavailable = 7200
	)
	var requeue bool
	availableCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorAvailable,
	}
	progressingCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorProgressing,
	}
	degradedCondition := configv1.ClusterOperatorStatusCondition{
		Type: configv1.OperatorDegraded,
	}

	if dsErr != nil {
		// There was a problem fetching Tuned daemonset
		if errors.IsNotFound(dsErr) {
			// Tuned daemonset has not been created yet
			if len(conditions) == 0 {
				// This looks like a fresh install => initialize
				glog.V(2).Infof("No ClusterOperator conditions set, initializing them.")
				availableCondition.Status = configv1.ConditionFalse
				availableCondition.Reason = "TunedUnavailable"
				availableCondition.Message = fmt.Sprintf("DaemonSet %q unavailable.", daemonset.Name)

				progressingCondition.Status = configv1.ConditionTrue
				progressingCondition.Reason = "Reconciling"
				progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))

				degradedCondition.Status = configv1.ConditionFalse
				degradedCondition.Reason = progressingCondition.Reason
				degradedCondition.Message = progressingCondition.Message
			} else {
				// This should not happen unless there was a manual intervention.
				// Preserve the previously known conditions and requeue.
				glog.Errorf("Unable to calculate Operator status conditions, preserving the old ones: %v", dsErr)
				return conditions, true
			}
		} else {
			glog.Errorf("Setting all ClusterOperator conditions to Unknown: %v", dsErr)
			availableCondition.Status = configv1.ConditionUnknown
			availableCondition.Reason = "Unknown"
			availableCondition.Message = fmt.Sprintf("Unable to fetch DaemonSet %q: %v", daemonset.Name, dsErr)

			progressingCondition.Status = availableCondition.Status
			progressingCondition.Reason = availableCondition.Reason
			progressingCondition.Message = availableCondition.Message

			degradedCondition.Status = availableCondition.Status
			degradedCondition.Reason = availableCondition.Reason
			degradedCondition.Message = availableCondition.Message
		}
	} else {
		if daemonset.Status.NumberAvailable > 0 {
			// The operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionTrue
			availableCondition.Reason = "AsExpected"
			if daemonset.Status.UpdatedNumberScheduled > 0 {
				// At least one operand instance runs RELEASE_VERSION, report it
				glog.V(2).Infof("%d operands run release version %q", daemonset.Status.UpdatedNumberScheduled, os.Getenv("RELEASE_VERSION"))
				availableCondition.Message = fmt.Sprintf("Cluster has deployed %q", os.Getenv("RELEASE_VERSION"))
			}
		} else {
			// No operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionFalse
			availableCondition.Reason = "TunedUnavailable"
			availableCondition.Message = fmt.Sprintf("DaemonSet %q has no available pod(s).", daemonset.Name)
			glog.V(2).Infof("syncOperatorStatus(): %s", availableCondition.Message)
		}

		// The operator is actively making changes to the operand (is Progressing) when:
		// the total number of nodes that should be running the daemon pod
		// (including nodes correctly running the daemon pod) != the total number of
		// nodes that are running updated daemon pod.
		if daemonset.Status.DesiredNumberScheduled != daemonset.Status.UpdatedNumberScheduled ||
			daemonset.Status.DesiredNumberScheduled == 0 {
			glog.V(2).Infof("Setting Progressing condition to true")
			progressingCondition.Status = configv1.ConditionTrue
			progressingCondition.Reason = "Reconciling"
			progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))
			requeue = true // Requeue as we need to set Progressing=false or Degraded=true eventually
		} else {
			progressingCondition.Status = configv1.ConditionFalse
			progressingCondition.Reason = "AsExpected"
			progressingCondition.Message = fmt.Sprintf("Cluster version is %q", os.Getenv("RELEASE_VERSION"))
		}

		degradedCondition.Status = configv1.ConditionFalse
		degradedCondition.Reason = "AsExpected"
		degradedCondition.Message = fmt.Sprintf("DaemonSet %q available", daemonset.Name)
	}

	conditions = clusteroperator.SetStatusCondition(conditions, &availableCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, &progressingCondition)

	// If the operator is not available for an extensive period of time, set the Degraded operator status
	now := metav1.Now().Unix()
	for _, condition := range conditions {
		if condition.Type == configv1.OperatorAvailable &&
			condition.Status == configv1.ConditionFalse &&
			now-condition.LastTransitionTime.Unix() > maxTunedUnavailable {
			glog.V(1).Infof("Operator unavailable for longer than %d seconds, setting Degraded status.", maxTunedUnavailable)
			degradedCondition.Status = configv1.ConditionTrue
			degradedCondition.Reason = "TunedUnavailable"
			degradedCondition.Message = fmt.Sprintf("DaemonSet %q unavailable for more than %d seconds.", maxTunedUnavailable)
		}
	}
	conditions = clusteroperator.SetStatusCondition(conditions, &degradedCondition)

	glog.V(2).Infof("Operator status conditions: %v", conditions)

	return conditions, requeue
}
