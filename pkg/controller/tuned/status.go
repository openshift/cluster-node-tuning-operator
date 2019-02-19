package tuned

import (
	"context"
	"fmt"

	"github.com/golang/glog"
	configv1 "github.com/openshift/api/config/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util/clusteroperator"
	"github.com/openshift/cluster-node-tuning-operator/version"
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
	r.client.Get(context.TODO(), types.NamespacedName{Namespace: dsManifest.Namespace, Name: dsManifest.Name}, daemonset)

	oldConditions := coState.Status.Conditions
	coState.Status.Conditions, requeue = computeStatusConditions(oldConditions, daemonset)
	operatorv1helpers.SetOperandVersion(&coState.Status.Versions, configv1.OperandVersion{Name: ntoconfig.OperatorName(), Version: version.Version})
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
func computeStatusConditions(conditions []configv1.ClusterOperatorStatusCondition, daemonset *appsv1.DaemonSet) ([]configv1.ClusterOperatorStatusCondition, bool) {
	var requeue bool
	availableCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorAvailable,
		Status: configv1.ConditionFalse,
	}
	progressingCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Status: configv1.ConditionFalse,
	}
	failingCondition := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorFailing,
		Status: configv1.ConditionFalse,
	}

	if daemonset == nil {
		availableCondition.Status = configv1.ConditionUnknown
		progressingCondition.Status = configv1.ConditionUnknown
		failingCondition.Status = configv1.ConditionUnknown
		requeue = true
	} else {
		if daemonset.Status.NumberUnavailable == 0 {
			availableCondition.Status = configv1.ConditionTrue
		} else {
			availableCondition.Status = configv1.ConditionFalse
			availableCondition.Message = fmt.Sprintf("DaemonSet %q has %d unavailable pod(s).", daemonset.Name, daemonset.Status.NumberUnavailable)
			progressingCondition.Status = configv1.ConditionTrue

		}
		requeue = daemonset.Status.NumberReady != daemonset.Status.DesiredNumberScheduled
	}

	conditions = clusteroperator.SetStatusCondition(conditions, availableCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, progressingCondition)
	conditions = clusteroperator.SetStatusCondition(conditions, failingCondition)

	return conditions, requeue
}
