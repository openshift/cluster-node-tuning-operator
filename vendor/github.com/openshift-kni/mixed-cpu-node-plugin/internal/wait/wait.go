package wait

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift-kni/mixed-cpu-node-plugin/internal/pods"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

func ForMCPUpdate(ctx context.Context, c client.Client, key client.ObjectKey) error {
	mcp := &machineconfigv1.MachineConfigPool{}
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, mcp)
		if err != nil {
			return false, fmt.Errorf("failed to get MCP %q; %w", mcp.Name, err)
		}
		for _, cond := range mcp.Status.Conditions {
			if cond.Type == machineconfigv1.MachineConfigPoolUpdating && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed wait for MCP %q transition to %q state; %w", mcp.Name, machineconfigv1.MachineConfigPoolUpdating, err)
	}

	err = wait.PollUntilContextTimeout(ctx, 30*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, mcp)
		if err != nil {
			return false, fmt.Errorf("failed to get MCP %q; %w", key.String(), err)
		}
		for _, cond := range mcp.Status.Conditions {
			if cond.Type == machineconfigv1.MachineConfigPoolUpdated && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed wait for MCP %q transition to %q state; %w", key.String(), machineconfigv1.MachineConfigPoolUpdated, err)
	}
	return nil
}

func ForDSReady(ctx context.Context, c client.Client, key client.ObjectKey) error {
	ds := &appsv1.DaemonSet{}
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, ds)
		if err != nil {
			return false, fmt.Errorf("failed to get daemonset %q; %w", key.String(), err)
		}
		if ds.Status.DesiredNumberScheduled == ds.Status.NumberReady {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed wait for daemonset %q to be ready. only %d/%d are ready; %w", key.String(), ds.Status.NumberReady, ds.Status.DesiredNumberScheduled, err)
	}
	return nil
}

func ForDeploymentReady(ctx context.Context, c client.Client, key client.ObjectKey) error {
	dp := &appsv1.Deployment{}
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, dp)
		if err != nil {
			return false, fmt.Errorf("failed to get deployment %q; %w", key.String(), err)
		}
		if dp.Status.Replicas == dp.Status.ReadyReplicas {
			for _, cond := range dp.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
		}
		return false, nil
	})

	if err != nil {
		// do not override the original error
		ownedPods, e := pods.OwnedByDeployment(ctx, c, dp, &client.ListOptions{})
		if e != nil {
			return e
		}
		return fmt.Errorf("failed wait for deployment %q to be ready. only %d/%d are ready;\n pods status: %s; %w",
			key.String(), dp.Status.ReadyReplicas, dp.Status.Replicas, pods.PrintStatus(ownedPods), err)
	}
	return nil
}

func ForNSDeletion(ctx context.Context, c client.Client, key client.ObjectKey) error {
	ns := &corev1.Namespace{}
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("failed to get namespace %q; %w", key.String(), err)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed wait for namespace %q deletion; %w", key.String(), err)
	}
	return nil
}

func ForNodeReady(ctx context.Context, c client.Client, key client.ObjectKey) error {
	node := &corev1.Node{}
	err := wait.PollUntilContextTimeout(ctx, 60*time.Second, 15*time.Minute, false, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, node)
		if err != nil {
			return false, fmt.Errorf("failed to get node %q; %w", key.String(), err)
		}
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				return cond.Status == corev1.ConditionTrue, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed wait for node %q to be ready; %w", key.String(), err)
	}
	return nil
}
