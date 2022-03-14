package tuned

import (
	"context"
	"fmt"
	"time"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
)

func WaitForAppliedCondition(tunedProfileNames []string, conditionStatus corev1.ConditionStatus, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		for _, tunedProfileName := range tunedProfileNames {
			profile := &tunedv1.Profile{}
			key := types.NamespacedName{
				Name:      tunedProfileName,
				Namespace: components.NamespaceNodeTuningOperator,
			}

			if err := testclient.Client.Get(context.TODO(), key, profile); err != nil {
				klog.Errorf("failed to get tuned profile %q: %v", tunedProfileName, err)
				return false, nil
			}

			appliedCondition, err := GetConditionByType(profile.Status.Conditions, tunedv1.TunedProfileApplied)
			if err != nil {
				klog.Errorf("failed to get applied condition for profile %q: %v", tunedProfileName, err)
				return false, nil
			}

			if appliedCondition.Status != conditionStatus {
				return false, nil
			}
		}

		return true, nil
	})
}

func GetConditionByType(conditions []tunedv1.ProfileStatusCondition, conditionType tunedv1.ProfileConditionType) (*tunedv1.ProfileStatusCondition, error) {
	for i := range conditions {
		c := &conditions[i]
		if c.Type == conditionType {
			return c, nil
		}
	}
	return nil, fmt.Errorf("failed to found applied condition under conditions %v", conditions)
}
