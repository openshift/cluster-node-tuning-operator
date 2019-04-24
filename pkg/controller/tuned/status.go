package tuned

import (
	"context"
	"fmt"
	"os"

	"github.com/golang/glog"
	configv1 "github.com/openshift/api/config/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
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

	dsManifest, err := r.manifestFactory.TunedDaemonSet()
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
		{
			Group:    "",
			Resource: "namespaces",
			Name:     dsManifest.Namespace,
		},
	}

	if clusteroperator.ConditionsEqual(oldConditions, coState.Status.Conditions) {
		return requeue, nil
	}

	_, err = r.cfgv1client.ClusterOperators().UpdateStatus(coState)
	if err != nil {
		return requeue, err
	}

	return requeue, nil
}

func (r *ReconcileTuned) getOrCreateOperatorStatus() (*configv1.ClusterOperator, error) {
	var err error

	clusterOperatorName := ntoconfig.OperatorName()

	co := &configv1.ClusterOperator{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterOperator",
			APIVersion: "config.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterOperatorName,
		},
	}

	if r.cfgv1client == nil {
		r.cfgv1client, err = ntoclient.GetCfgV1Client()
		if r.cfgv1client == nil {
			return nil, err
		}
	}

	coGet, err := r.cfgv1client.ClusterOperators().Get(clusterOperatorName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Cluster operator not found, create it
			co_created, err := r.cfgv1client.ClusterOperators().Create(co)
			if err != nil {
				// Failed to create the cluster operator object
				return nil, err
			}
			return co_created, nil
		} else {
			return nil, err
		}
	}
	return coGet, nil
}

// computeStatusConditions computes the operator's current state.
func computeStatusConditions(conditions []configv1.ClusterOperatorStatusCondition,
	daemonset *appsv1.DaemonSet,
	dsErr error) ([]configv1.ClusterOperatorStatusCondition, bool) {
	var requeue bool
	availableCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorAvailable,
		Status: configv1.ConditionFalse,
	}
	progressingCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Status: configv1.ConditionFalse,
	}
	degradedCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorDegraded,
		Status: configv1.ConditionFalse,
	}

	if dsErr != nil {
		// There was a problem fetching Tuned daemonset
		if errors.IsNotFound(dsErr) {
			// Tuned daemonset has not been created yet
			if len(conditions) == 0 {
				// This looks like a fresh install => initialize
				glog.V(2).Infof("No ClusterOperator conditions set, initializing them.")
				availableCondition.Status = configv1.ConditionFalse
				progressingCondition.Status = configv1.ConditionTrue
				progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))
				degradedCondition.Status = configv1.ConditionFalse
			} else {
				// This should not happen unless there was a manual intervention.
				// Preserve the previously known conditions and requeue.
				glog.Errorf("Unable to calculate Operator status conditions, preserving the old ones: %v", dsErr)
				return conditions, true
			}
		} else {
			// Unclassified error fetching the Tuned daemonset
			glog.Errorf("Setting all ClusterOperator conditions to Unknown: ", dsErr)
			availableCondition.Status = configv1.ConditionUnknown
			progressingCondition.Status = configv1.ConditionUnknown
			degradedCondition.Status = configv1.ConditionUnknown
		}
	} else {
		if daemonset.Status.NumberAvailable > 0 {
			// The operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionTrue
			if daemonset.Status.UpdatedNumberScheduled > 0 {
				// At least one operand instance runs RELEASE_VERSION, report it
				glog.V(2).Infof("%d operands run release version %q", daemonset.Status.UpdatedNumberScheduled, os.Getenv("RELEASE_VERSION"))
				availableCondition.Message = fmt.Sprintf("Cluster has deployed %q", os.Getenv("RELEASE_VERSION"))
			}
		} else {
			// No operand maintained by the operator is reported as available in the cluster
			availableCondition.Status = configv1.ConditionFalse
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
			progressingCondition.Message = fmt.Sprintf("Working towards %q", os.Getenv("RELEASE_VERSION"))
			requeue = true // Requeue as we need to set Progressing=false or Failing=true eventually
		} else {
			progressingCondition.Message = fmt.Sprintf("Cluster version is %q", os.Getenv("RELEASE_VERSION"))
		}
	}

	conditions = clusteroperator.SetStatusCondition(conditions, availableCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, progressingCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, degradedCondition)
	glog.V(2).Infof("Operator status conditions: %v", conditions)

	return conditions, requeue
}
