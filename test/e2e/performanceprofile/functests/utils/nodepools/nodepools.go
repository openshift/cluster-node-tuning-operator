package nodepools

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func WaitForUpdatingConfig(ctx context.Context, c client.Client, NpName, namespace string) error {
	return waitForCondition(ctx, c, NpName, namespace, func(conds []hypershiftv1beta1.NodePoolCondition) bool {
		for _, cond := range conds {
			if cond.Type == hypershiftv1beta1.NodePoolUpdatingConfigConditionType {
				return cond.Status == corev1.ConditionTrue
			}
		}
		return false
	})
}

func WaitForConfigToBeReady(ctx context.Context, c client.Client, NpName, namespace string) error {
	return waitForCondition(ctx, c, NpName, namespace, func(conds []hypershiftv1beta1.NodePoolCondition) bool {
		for _, cond := range conds {
			if cond.Type == hypershiftv1beta1.NodePoolUpdatingConfigConditionType {
				return cond.Status == corev1.ConditionFalse
			}
		}
		return false
	})
}

func waitForCondition(ctx context.Context, c client.Client, NpName, namespace string, conditionFunc func([]hypershiftv1beta1.NodePoolCondition) bool) error {
	return wait.PollUntilContextTimeout(ctx, time.Second*10, time.Minute*60, false, func(ctx context.Context) (done bool, err error) {
		np := &hypershiftv1beta1.NodePool{}
		key := client.ObjectKey{Name: NpName, Namespace: namespace}
		err = c.Get(ctx, key, np)
		if err != nil {
			return false, err
		}
		return conditionFunc(np.Status.Conditions), nil
	})
}

func NodePoolStatusMessages(ctx context.Context, c client.Client, NpName, namespace string, conditionReason string) ([]string, error) {
	var messages []string
	condFunc := func(conds []hypershiftv1beta1.NodePoolCondition) bool {
		for _, cond := range conds {
			if cond.Reason == conditionReason {
				messages = append(messages, cond.Message)
			}
		}
		return len(messages) > 0
	}
	if err := waitForCondition(ctx, c, NpName, namespace, condFunc); err != nil {
		return nil, err
	}
	return messages, nil
}

func GetByClusterName(ctx context.Context, c client.Client, hostedClusterName string) (*hypershiftv1beta1.NodePool, error) {
	npList := &hypershiftv1beta1.NodePoolList{}
	if err := c.List(ctx, npList); err != nil {
		return nil, err
	}
	var np *hypershiftv1beta1.NodePool
	for i := 0; i < len(npList.Items); i++ {
		if npList.Items[i].Spec.ClusterName == hostedClusterName {
			np = &npList.Items[i]
			break
		}
	}
	if np == nil {
		return nil, fmt.Errorf("failed to find nodePool associated with cluster %q; existing nodePools are: %+v", hostedClusterName, npList.Items)
	}
	return np, nil
}

func AttachConfigObject(ctx context.Context, cli client.Client, object client.Object) error {
	np, err := GetNodePool(ctx, cli)
	if err != nil {
		return err
	}

	return AttachConfigObjectToNodePool(ctx, cli, object, np)
}

func AttachConfigObjectToNodePool(ctx context.Context, cli client.Client, object client.Object, np *hypershiftv1beta1.NodePool) error {
	var err error
	updatedConfig := []corev1.LocalObjectReference{{Name: object.GetName()}}
	for i := range np.Spec.Config {
		Config := np.Spec.Config[i]
		if Config.Name != object.GetName() {
			updatedConfig = append(updatedConfig, Config)
		}
	}
	np.Spec.Config = updatedConfig
	if err = cli.Update(ctx, np); err != nil {
		return err
	}
	return nil
}

func DeattachConfigObject(ctx context.Context, cli client.Client, object client.Object) error {
	np, err := GetNodePool(ctx, cli)
	if err != nil {
		return err
	}

	return DeattachConfigObjectFromNodePool(ctx, cli, object, np)
}

func DeattachConfigObjectFromNodePool(ctx context.Context, cli client.Client, object client.Object, np *hypershiftv1beta1.NodePool) error {
	var err error
	for i := range np.Spec.Config {
		if np.Spec.Config[i].Name == object.GetName() {
			np.Spec.Config = append(np.Spec.Config[:i], np.Spec.Config[i+1:]...)
			break
		}
	}
	if err = cli.Update(ctx, np); err != nil {
		return err
	}
	return nil
}

// AttachTuningObject is attaches a tuning object into the nodepool associated with the hosted-cluster
// The function is idempotent
func AttachTuningObject(ctx context.Context, cli client.Client, object client.Object) error {
	np, err := GetNodePool(ctx, cli)
	if err != nil {
		return err
	}

	return AttachTuningObjectToNodePool(ctx, cli, object, np)
}

func AttachTuningObjectToNodePool(ctx context.Context, cli client.Client, object client.Object, np *hypershiftv1beta1.NodePool) error {
	var err error
	updatedTuningConfig := []corev1.LocalObjectReference{{Name: object.GetName()}}
	for i := range np.Spec.TuningConfig {
		tuningConfig := np.Spec.TuningConfig[i]
		if tuningConfig.Name != object.GetName() {
			updatedTuningConfig = append(updatedTuningConfig, tuningConfig)
		}
	}
	np.Spec.TuningConfig = updatedTuningConfig
	if cli.Update(ctx, np) != nil {
		return err
	}
	return nil
}

func DeattachTuningObject(ctx context.Context, cli client.Client, object client.Object) error {
	np, err := GetNodePool(ctx, cli)
	if err != nil {
		return err
	}

	return DeattachTuningObjectToNodePool(ctx, cli, object, np)
}

func DeattachTuningObjectToNodePool(ctx context.Context, cli client.Client, object client.Object, np *hypershiftv1beta1.NodePool) error {
	var err error
	for i := range np.Spec.TuningConfig {
		if np.Spec.TuningConfig[i].Name == object.GetName() {
			np.Spec.TuningConfig = append(np.Spec.TuningConfig[:i], np.Spec.TuningConfig[i+1:]...)
			break
		}
	}
	if cli.Update(ctx, np) != nil {
		return err
	}
	return nil
}

func GetNodePool(ctx context.Context, cli client.Client) (*hypershiftv1beta1.NodePool, error) {
	hostedClusterName, err := hypershift.GetHostedClusterName()
	if err != nil {
		return nil, err
	}
	np, err := GetByClusterName(ctx, cli, hostedClusterName)
	if err != nil {
		return nil, err
	}
	return np, nil
}
