package daemonset

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/controller-runtime/pkg/client"

	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

func WaitToBeRunning(ctx context.Context, cli client.Client, namespace, name string) error {
	return WaitToBeRunningWithTimeout(ctx, cli, namespace, name, 5*time.Minute)
}

func WaitToBeRunningWithTimeout(ctx context.Context, cli client.Client, namespace, name string, timeout time.Duration) error {
	testlog.Infof("wait for the daemonset %q %q to be running", namespace, name)
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx2 context.Context) (bool, error) {
		return IsRunning(ctx2, cli, namespace, name)
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
	testlog.Infof("daemonset %q %q desired %d scheduled %d ready %d", namespace, name, ds.Status.DesiredNumberScheduled, ds.Status.CurrentNumberScheduled, ds.Status.NumberReady)
	return (ds.Status.DesiredNumberScheduled > 0 && ds.Status.DesiredNumberScheduled == ds.Status.NumberReady), nil
}
