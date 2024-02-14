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
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
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
	store := map[string]*string{
		"cpuset.cpus":           &cfg.Cpus,
		"cpuset.cpus.exclusive": &cfg.Exclusive,
		"cpuset.cpus.effective": &cfg.Effective,
		"cpuset.cpus.partition": &cfg.Partition,
		"cpuset.mems":           &cfg.Mems,
	}
	err := cm.execAndStore(pod, containerName, dirPath, store)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
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
	cfg.Stat, err = stat(cm.k8sClient, pod, containerName, childName)
	return cfg, nil
}

// stat fetch cpu.stat values
func stat(k8sclient *kubernetes.Clientset, pod *corev1.Pod, containerName, childName string) (map[string]string, error) {
	cpuStat := make(map[string]string)
	dirPath := path.Join(controller.CgroupMountPoint, childName)
	cmd := []string{
		"/bin/cat",
		dirPath + "/cpu.stat",
	}
	statBytes, err := pods.ExecCommandOnPod(k8sclient, pod, containerName, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cgroup config for pod. pod=%q, container=%q; %w", client.ObjectKeyFromObject(pod).String(), containerName, err)
	}
	output := strings.TrimSpace(string(statBytes))
	interfacevalues := strings.Split(output, "\r\n")
	// cpu.stat always contains 3 stats usage_usec, user_usec, system_usec
	// only when cpu controller is enabled other stats like nr_periods etc are enabled
	// this is not applicable for v1 as controllers are preenabled by default
	if len(interfacevalues) < 4 {
		return nil, fmt.Errorf("CPU Controller is not enabled")
	}
	for _, v := range interfacevalues {
		values := strings.Split(v, " ")
		cpuStat[values[0]] = values[1]
	}
	return cpuStat, nil
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
func (cm *ControllersManager) execAndStore(pod *corev1.Pod, containerName, dirPath string, store map[string]*string) error {
	for k, v := range store {
		fullPath := dirPath + "/" + k
		cmd := []string{
			"/bin/cat",
			fullPath,
		}
		b, err := pods.ExecCommandOnPod(cm.k8sClient, pod, containerName, cmd)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			log.Warningf("empty value in cgroupv2 controller file; pod=%q,container=%q,file=%q", pod.Name, containerName, fullPath)
			*v = ""
			continue
		}
		output := strings.Trim(string(b), "\r\n")
		*v = output
	}
	return nil
}
