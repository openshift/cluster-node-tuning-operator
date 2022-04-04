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

func WaitToBeRunning(cli client.Client, namespace, name string) error {
	return WaitToBeRunningWithTimeout(cli, namespace, name, 5*time.Minute)
}

func WaitToBeRunningWithTimeout(cli client.Client, namespace, name string, timeout time.Duration) error {
	testlog.Infof("wait for the daemonset %q %q to be running", namespace, name)
	return wait.PollImmediate(10*time.Second, timeout, func() (bool, error) {
		return IsRunning(cli, namespace, name)
	})
}

func GetByName(cli client.Client, namespace, name string) (*appsv1.DaemonSet, error) {
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	var ds appsv1.DaemonSet
	err := cli.Get(context.TODO(), key, &ds)
	return &ds, err
}

func IsRunning(cli client.Client, namespace, name string) (bool, error) {
	ds, err := GetByName(cli, namespace, name)
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
