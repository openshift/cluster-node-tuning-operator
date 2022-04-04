package images

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testds "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/daemonset"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

const (
	PrePullPrefix                = "prepull"
	PrePullDefaultTimeoutMinutes = "5"
)

// GetPullTimeout returns the pull timeout
func GetPullTimeout() (time.Duration, error) {
	prePullTimeoutMins, ok := os.LookupEnv("PREPULL_IMAGE_TIMEOUT_MINUTES")
	if !ok {
		prePullTimeoutMins = PrePullDefaultTimeoutMinutes
	}
	timeout, err := strconv.Atoi(prePullTimeoutMins)
	return time.Duration(timeout) * time.Minute, err
}

// PrePull makes sure the image is pre-pulled on the relevant nodes.
func PrePull(cli client.Client, pullSpec, namespace, tag string) (*appsv1.DaemonSet, error) {
	name := PrePullPrefix + tag
	ds := appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "prepull-daemonset-" + tag,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "prepull-daemonset-" + tag,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "prepullcontainer",
							Image:           pullSpec,
							Command:         []string{"/bin/sleep"},
							Args:            []string{"inf"},
							ImagePullPolicy: corev1.PullAlways,
						},
					},
				},
			},
		},
	}

	prePullTimeout, err := GetPullTimeout()
	if err != nil {
		return &ds, err
	}
	testlog.Infof("pull timeout: %v", prePullTimeout)

	testlog.Infof("creating daemonset %s/%s to prepull %q", namespace, name, pullSpec)
	ts := time.Now()
	err = cli.Create(context.TODO(), &ds)
	if err != nil {
		return &ds, err
	}
	data, _ := json.Marshal(ds)
	testlog.Infof("created daemonset %s/%s to prepull %q:\n%s", namespace, name, pullSpec, string(data))

	err = testds.WaitToBeRunningWithTimeout(testclient.Client, ds.Namespace, ds.Name, prePullTimeout)
	if err != nil {
		// if this fails, no big deal, we are just trying to make the troubleshooting easier
		updatedDs, _ := testds.GetByName(testclient.Client, ds.Namespace, ds.Name)
		return updatedDs, err
	}
	testlog.Infof("prepulled %q in %v", pullSpec, time.Since(ts))
	return nil, nil
}
