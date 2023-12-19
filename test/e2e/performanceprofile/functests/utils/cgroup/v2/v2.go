package v2

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
		dirPath + "/cpuset.cpus",
		dirPath + "/cpuset.cpus.exclusive",
		dirPath + "/cpuset.cpus.effective",
		dirPath + "/cpuset.cpus.partition",
		dirPath + "/cpuset.mems",
	}
	b, err := pods.ExecCommandOnPod(cm.k8sClient, pod, containerName, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
	output := strings.Split(string(b), "\r\n")
	cfg.Cpus = output[0]
	cfg.Exclusive = output[1]
	cfg.Effective = output[2]
	cfg.Partition = output[3]
	cfg.Mems = output[4]
	return cfg, nil
}

func (cm *ControllersManager) Cpu(ctx context.Context, pod *corev1.Pod, containerName, childName, runtimeType string) (*controller.Cpu, error) {
	cfg := &controller.Cpu{}
	dirPath := path.Join(controller.CgroupMountPoint, childName)
	cmd := []string{
		"/bin/cat",
		dirPath + "/cpu.max",
	}
	b, err := pods.ExecCommandOnPod(cm.k8sClient, pod, containerName, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
	output := strings.Split(string(b), "\r\n")
	quotaAndPeriod := strings.Split(output[0], " ")
	cfg.Quota = quotaAndPeriod[0]
	cfg.Period = quotaAndPeriod[1]
	return cfg, nil
}

func (cm *ControllersManager) Pod(ctx context.Context, pod *corev1.Pod, controllerConfig interface{}) error {
	// TODO
	return nil
}

func (cm *ControllersManager) Container(ctx context.Context, pod *corev1.Pod, containerName string, controllerConfig interface{}) error {
	return cm.Child(ctx, pod, containerName, "", controllerConfig)
}

func (cm *ControllersManager) Child(ctx context.Context, pod *corev1.Pod, containerName, childName string, controllerConfig interface{}) error {
	runtimeType, err := runtime.GetContainerRuntimeTypeFor(ctx, cm.client, pod)
	if err != nil {
		return err
	}
	switch cc := controllerConfig.(type) {
	case *controller.CpuSet:
		cfg, err := cm.CpuSet(ctx, pod, containerName, childName, runtimeType)
		if err != nil {
			return err
		}
		*cc = *cfg
	case *controller.Cpu:
		cfg, err := cm.Cpu(ctx, pod, containerName, childName, runtimeType)
		if err != nil {
			return err
		}
		*cc = *cfg
	default:
		return fmt.Errorf("failed to get the controller config type")
	}
	return nil
}
