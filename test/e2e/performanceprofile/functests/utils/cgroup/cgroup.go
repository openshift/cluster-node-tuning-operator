package cgroup

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiconfigv1 "github.com/openshift/api/config/v1"
	cgroupv1 "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/v1"
	cgroupv2 "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/v2"
)

type ControllersGetter interface {
	// Pod is for getting controller config at the pod level
	Pod(ctx context.Context, pod *corev1.Pod, controllerConfig interface{}) error
	// Container is for getting controller config at the container level
	Container(ctx context.Context, pod *corev1.Pod, containerName string, controllerConfig interface{}) error
	// Child is for getting controller config at the container's child level
	Child(ctx context.Context, pod *corev1.Pod, containerName, childName string, controllerConfig interface{}) error
}

func IsVersion2(ctx context.Context, c client.Client) (bool, error) {
	nodecfg := &apiconfigv1.Node{}
	key := client.ObjectKey{
		Name: "cluster",
	}
	err := c.Get(ctx, key, nodecfg)
	if err != nil {
		return false, fmt.Errorf("failed to get configs.node object. name=%q; %w", key.Name, err)
	}
	if nodecfg.Spec.CgroupMode == apiconfigv1.CgroupModeV1 {
		return false, nil
	}
	// in case `apiconfigv1.CgroupMode` not set, it's returned true since
	// the platform's default is cgroupV2
	return true, nil
}

// BuildGetter return a cgroup information getter that complies with
// the cgroup version that is currently used by the nodes on the cluster
func BuildGetter(ctx context.Context, c client.Client, k8sClient *kubernetes.Clientset) (ControllersGetter, error) {
	isV2, versionErr := IsVersion2(ctx, c)
	if versionErr != nil {
		return nil, versionErr
	}
	if isV2 {
		return cgroupv2.NewManager(c, k8sClient), nil
	} else {
		return cgroupv1.NewManager(c, k8sClient), nil
	}
}
