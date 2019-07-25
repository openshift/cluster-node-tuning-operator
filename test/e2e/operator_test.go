// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestOperatorAvailable(t *testing.T) {
	client := ntoclient.GetClient()
	if client == nil {
		t.Fatal("Failed to get kube client.")
	}

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		co := &configv1.ClusterOperator{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: ntoconfig.OperatorName()}, co); err != nil {
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == configv1.OperatorAvailable &&
				cond.Status == configv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("Did not get expected available condition: %v", err)
	}
}

func TestDefaultTunedExists(t *testing.T) {
	client := ntoclient.GetClient()
	if client == nil {
		t.Fatal("Failed to get kube client.")
	}

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		tuned := &tunedv1.Tuned{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: "default", Namespace: ntoconfig.OperatorNamespace()}, tuned); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("Failed to get default tuned: %v", err)
	}
}
