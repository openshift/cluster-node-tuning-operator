package nodepools

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func GetByClusterName(ctx context.Context, c client.Client, hostedClusterName string) (*hypershiftv1beta1.NodePool, error) {
	npList := &hypershiftv1beta1.NodePoolList{}
	if err := c.List(ctx, npList); err != nil {
		return nil, err
	}
	var np *hypershiftv1beta1.NodePool
	for i := 0; i < len(npList.Items); i++ {
		if npList.Items[i].Spec.ClusterName == hostedClusterName {
			np = &npList.Items[i]
		}
	}
	if np == nil {
		return nil, fmt.Errorf("failed to find nodePool associated with cluster %q; existing nodePools are: %+v", hostedClusterName, npList.Items)
	}
	return np, nil
}
