package nodepools

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/labels"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	apiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	nodePoolAnnotationTargetConfigVersion  = "hypershift.openshift.io/nodePoolTargetConfigVersion"
	// machineSetLabelKey is the key name in the machineSet label
	// which associates the machineSet with the hosted cluster.
	// The value is the hosted cluster name
	machineSetLabelKey = "cluster.x-k8s.io/cluster-name"
)

func WaitForUpdatingConfig(ctx context.Context, c client.Client, name, namespace string) error {
	return wait.PollUntilContextTimeout(ctx, time.Second*10, time.Minute*20, false, func(ctx context.Context) (done bool, err error) {
		np := &hypershiftv1beta1.NodePool{}
		key := client.ObjectKey{Name: name, Namespace: namespace}
		err = c.Get(ctx, key, np)
		if err != nil {
			return false, fmt.Errorf("failed to Get nodePool %q; %v", key.String(), err)
		}
		for _, cond := range np.Status.Conditions {
			if cond.Type == "UpdatingConfig" {
				return cond.Status == corev1.ConditionTrue, nil
			}
		}
		return false, nil
	})
}

func WaitForConfigToBeReady(ctx context.Context, c client.Client, hostedClusterName, namespace string) error {
	return wait.PollUntilContextTimeout(ctx, time.Second*10, time.Minute*20, false, func(ctx context.Context) (done bool, err error) {
		msList := &apiv1beta1.MachineSetList{}
		opts := &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{machineSetLabelKey: hostedClusterName}),
		}
		err = c.List(ctx, msList, opts)
		if err != nil {
			return false, fmt.Errorf("failed to List machineSet with label %q; %v", opts.LabelSelector.String(), err)
		}
		if len(msList.Items) == 0 {
			return false, fmt.Errorf("machineSetList with label %q is empty", opts.LabelSelector.String())
		}
		ms := msList.Items[0]
		annot := ms.Annotations
		// check that the machineSet has been updated with the desired (TargetConfig) version
		if annot[nodePoolAnnotationCurrentConfigVersion] == annot[nodePoolAnnotationTargetConfigVersion] {
			return true, nil
		}
		return false, nil
	})
}
