package daemonset

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

func WaitToBeRunning(ctx context.Context, cli client.Client, namespace, name string) error {
	return WaitToBeRunningWithTimeout(ctx, cli, namespace, name, 5*time.Minute)
}

func WaitToBeRunningWithTimeout(ctx context.Context, cli client.Client, namespace, name string, timeout time.Duration) error {
	testlog.Infof("wait for the daemonset %q %q to be running", namespace, name)
	return wait.PollUntilContextTimeout(ctx, 30*time.Second, timeout, true, func(derivedCtx context.Context) (bool, error) {
		return IsRunning(derivedCtx, cli, namespace, name)
	})
}

func GetByName(ctx context.Context, cli client.Client, namespace, name string) (*appsv1.DaemonSet, error) {
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	var ds appsv1.DaemonSet
	err := cli.Get(ctx, key, &ds)
	return &ds, err
}

func IsRunning(ctx context.Context, cli client.Client, namespace, name string) (bool, error) {
	ds, err := GetByName(ctx, cli, namespace, name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			testlog.Warningf("daemonset %q %q not found - retrying", namespace, name)
			return false, nil
		}
		return false, err
	}

	// Enhanced logging for debugging
	testlog.Infof("DaemonSet %s/%s status: Desired=%d, Current=%d, Ready=%d, Available=%d, Updated=%d",
		namespace, name, ds.Status.DesiredNumberScheduled, ds.Status.CurrentNumberScheduled,
		ds.Status.NumberReady, ds.Status.NumberAvailable, ds.Status.UpdatedNumberScheduled)

	if isRunning := ds.Status.DesiredNumberScheduled > 0 && ds.Status.DesiredNumberScheduled == ds.Status.NumberReady; !isRunning {
		if ds.Status.DesiredNumberScheduled == 0 {
			testlog.Warningf("DaemonSet %s/%s has 0 desired pods - no nodes match the selector or all nodes are unschedulable", namespace, name)
		} else if ds.Status.NumberReady < ds.Status.DesiredNumberScheduled {
			testlog.Warningf("DaemonSet %s/%s: only %d/%d pods are ready", namespace, name, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
		}
		return false, logEventsAndPhase(ctx, cli, ds)
	}
	testlog.Infof("DaemonSet %s/%s is running successfully", namespace, name)
	return true, nil
}

func logEventsAndPhase(ctx context.Context, cli client.Client, ds *appsv1.DaemonSet) error {
	podList := &corev1.PodList{}
	err := cli.List(ctx, podList, &client.ListOptions{
		Namespace:     ds.Namespace,
		LabelSelector: labels.SelectorFromSet(ds.Spec.Selector.MatchLabels),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods for DaemonSet %s; %w", client.ObjectKeyFromObject(ds).String(), err)
	}
	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning {
			podKey := client.ObjectKeyFromObject(&pod).String()
			testlog.Warningf("daemonset %s pod %s is not running, expected status %s got %s", ds.Name, podKey, corev1.PodRunning, pod.Status.Phase)
			podEvents, err := events.GetEventsForObject(cli, pod.Namespace, pod.Name, string(pod.UID))
			if err != nil {
				return fmt.Errorf("failed to list events for Pod %s; %w", podKey, err)
			}
			for _, event := range podEvents.Items {
				testlog.Warningf("-> %s %s %s", event.Action, event.Reason, event.Message)
			}
		}
	}
	return nil
}
