package profiles

import (
	"context"
	"fmt"
	"reflect"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	v1 "github.com/openshift/custom-resource-status/conditions/v1"
)

// GetByNodeLabels gets the performance profile that must have node selector equals to passed node labels
func GetByNodeLabels(nodeLabels map[string]string) (*performancev2.PerformanceProfile, error) {
	profiles, err := All()
	if err != nil {
		return nil, err
	}

	var result *performancev2.PerformanceProfile
	for i := 0; i < len(profiles.Items); i++ {
		if reflect.DeepEqual(profiles.Items[i].Spec.NodeSelector, nodeLabels) {
			if result != nil {
				return nil, fmt.Errorf("found more than one performance profile with specified node selector %v", nodeLabels)
			}
			result = &profiles.Items[i]
		}
	}

	if result == nil {
		return nil, fmt.Errorf("failed to find performance profile with specified node selector %v", nodeLabels)
	}

	return result, nil
}

// WaitForDeletion waits until the pod will be removed from the cluster
func WaitForDeletion(profileKey types.NamespacedName, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		prof := &performancev2.PerformanceProfile{}
		if err := testclient.Client.Get(context.TODO(), profileKey, prof); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// GetCondition the performance profile condition for the given type
func GetCondition(nodeLabels map[string]string, conditionType v1.ConditionType) *v1.Condition {
	profile, err := GetByNodeLabels(nodeLabels)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "Failed getting profile by nodelabels %v", nodeLabels)
	for _, condition := range profile.Status.Conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

// GetConditionMessage gets the performance profile message for the given type
func GetConditionMessage(nodeLabels map[string]string, conditionType v1.ConditionType) string {
	cond := GetCondition(nodeLabels, conditionType)
	if cond != nil {
		return cond.Message
	}
	return ""
}

func GetConditionWithStatus(nodeLabels map[string]string, conditionType v1.ConditionType) *v1.Condition {
	var cond *v1.Condition
	EventuallyWithOffset(1, func() bool {
		cond = GetCondition(nodeLabels, conditionType)
		if cond == nil {
			return false
		}
		return cond.Status == corev1.ConditionTrue
	}, 30, 5).Should(BeTrue(), "condition %q not matched: %#v", conditionType, cond)
	return cond
}

// All gets all the exiting profiles in the cluster
func All() (*performancev2.PerformanceProfileList, error) {
	profiles := &performancev2.PerformanceProfileList{}
	if err := testclient.Client.List(context.TODO(), profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func UpdateWithRetry(profile *performancev2.PerformanceProfile) {
	EventuallyWithOffset(1, func() error {
		updatedProfile := &performancev2.PerformanceProfile{}
		key := types.NamespacedName{
			Name:      profile.Name,
			Namespace: profile.Namespace,
		}
		// We should get the updated version of the performance profile.
		// Otherwise, we will always try to update the profile with the old resource version
		// and will always get the conflict error
		if err := testclient.Client.Get(context.TODO(), key, updatedProfile); err != nil {
			return err
		}

		updatedProfile.Spec = *profile.Spec.DeepCopy()
		if err := testclient.Client.Update(context.TODO(), profile); err != nil {
			if !errors.IsConflict(err) {
				testlog.Errorf("failed to update the profile %q: %v", profile.Name, err)
			}

			return err
		}

		return nil
	}, time.Minute, 5*time.Second).Should(BeNil())
}

func WaitForCondition(nodeLabels map[string]string, conditionType v1.ConditionType, conditionStatus corev1.ConditionStatus) {
	EventuallyWithOffset(1, func() corev1.ConditionStatus {
		return (GetCondition(nodeLabels, conditionType)).Status
	}, 15*time.Minute, 30*time.Second).Should(Equal(conditionStatus), "Failed to met performance profile condition %v", conditionType)
}

// Delete delete the existing profile by name
func Delete(name string) error {
	profile := &performancev2.PerformanceProfile{}
	if err := testclient.Client.Get(context.TODO(), types.NamespacedName{Name: name}, profile); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if err := testclient.Client.Delete(context.TODO(), profile); err != nil {
		return err
	}

	return nil
}
