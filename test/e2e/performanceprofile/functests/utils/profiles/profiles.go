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

	"sigs.k8s.io/controller-runtime/pkg/client"

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
	return wait.PollUntilContextTimeout(context.TODO(), time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		prof := &performancev2.PerformanceProfile{}
		if err := testclient.ControlPlaneClient.Get(ctx, profileKey, prof); errors.IsNotFound(err) {
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
	if err := testclient.ControlPlaneClient.List(context.TODO(), profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func UpdateWithRetry(profile *performancev2.PerformanceProfile) {
	EventuallyWithOffset(1, func() error {
		profileFromAPIServer := &performancev2.PerformanceProfile{}
		// get the current resourceVersion
		if err := testclient.ControlPlaneClient.Get(context.TODO(), client.ObjectKeyFromObject(profile), profileFromAPIServer); err != nil {
			return err
		}
		prepared := prepareForUpdate(profile, profileFromAPIServer)
		if err := testclient.ControlPlaneClient.Update(context.TODO(), prepared); err != nil {
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
	if err := testclient.ControlPlaneClient.Get(context.TODO(), types.NamespacedName{Name: name}, profile); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if err := testclient.ControlPlaneClient.Delete(context.TODO(), profile); err != nil {
		return err
	}
	key := client.ObjectKey{
		Name: name,
	}
	return WaitForDeletion(key, 2*time.Minute)
}

func prepareForUpdate(updated, current *performancev2.PerformanceProfile) *performancev2.PerformanceProfile {
	current.Spec = updated.Spec
	if updated.Labels != nil {
		current.Labels = updated.Labels
	}
	current.Annotations = updated.DeepCopy().Annotations

	// we return current since it has the most updated data from the API server
	return current
}
