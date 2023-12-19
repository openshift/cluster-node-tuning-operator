package v1

import (
	"context"
	"fmt"
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/runtime"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
)

type ControllersManager struct {
	client    client.Client
	k8sClient *kubernetes.Clientset
}

func NewManager(c client.Client, k8sClient *kubernetes.Clientset) *ControllersManager {
	return &ControllersManager{client: c, k8sClient: k8sClient}
}

func (cm *ControllersManager) CpuSet(ctx context.Context, pod *corev1.Pod, containerName, childName, runtimeType string) (*controller.CpuSet, error) {
	cfg := &controller.CpuSet{}
	dirPath := path.Join(controller.CgroupMountPoint, childName)
	cmd := []string{
		"/bin/cat",
		dirPath + "/cpuset/cpuset.cpus",
		dirPath + "/cpuset/cpuset.cpu_exclusive",
		dirPath + "/cpuset/cpuset.effective_cpus",
		dirPath + "/cpuset/cpuset.mems",
		dirPath + "/cpuset/cpuset.sched_load_balance",
	}
	b, err := pods.ExecCommandOnPod(cm.k8sClient, pod, containerName, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
	output := strings.Split(string(b), "\r\n")
	cfg.Cpus = output[0]
	cfg.Exclusive = output[1]
	cfg.Effective = output[2]
	cfg.Mems = output[3]
	cfg.SchedLoadBalance = output[4] == "1"
	return cfg, nil
}

func (cm *ControllersManager) Cpu(ctx context.Context, pod *corev1.Pod, containerName, childName, runtimeType string) (*controller.Cpu, error) {
	cfg := &controller.Cpu{}
	dirPath := path.Join(controller.CgroupMountPoint, childName)
	cmd := []string{
		"/bin/cat",
		dirPath + "/cpu/cpu.cfs_quota_us",
		dirPath + "/cpu/cpu.cfs_period_us",
	}
	b, err := pods.ExecCommandOnPod(cm.k8sClient, pod, containerName, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
	output := strings.Split(string(b), "\r\n")
	cfg.Quota = output[0]
	cfg.Period = output[1]
	return cfg, nil
}

func (cm *ControllersManager) Pod(ctx context.Context, pod *corev1.Pod, controllerConfig interface{}) error {
	// TODO
	return nil
}

func (cm *ControllersManager) Container(ctx context.Context, pod *corev1.Pod, containerName string, controllerConfig interface{}) error {
	runtimeType, err := runtime.GetContainerRuntimeTypeFor(ctx, cm.client, pod)
	if err != nil {
		return err
	}
	switch cc := controllerConfig.(type) {
	case *controller.CpuSet:
		cfg, err := cm.CpuSet(ctx, pod, containerName, "", runtimeType)
		if err != nil {
			return err
		}
		*cc = *cfg
	case *controller.Cpu:
		cfg, err := cm.Cpu(ctx, pod, containerName, "", runtimeType)
		if err != nil {
			return err
		}
		*cc = *cfg
	default:
		return fmt.Errorf("failed to get the controller config type")
	}
	return err
}

func (cm *ControllersManager) Child(ctx context.Context, pod *corev1.Pod, containerName, childName string, controllerConfig interface{}) error {
	// TODO
	return nil
}
