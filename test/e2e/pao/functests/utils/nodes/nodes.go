package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/gomega"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/openshift-kni/performance-addon-operators/functests/utils"
	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/cluster"
	testlog "github.com/openshift-kni/performance-addon-operators/functests/utils/log"
	testpods "github.com/openshift-kni/performance-addon-operators/functests/utils/pods"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
)

const (
	testTimeout      = 480
	testPollInterval = 2
)

const (
	sysDevicesOnlineCPUs = "/sys/devices/system/cpu/online"
)

// NumaNodes defines cpus in each numa node
type NumaNodes struct {
	Cpus []NodeCPU `json:"cpus"`
}

// NodeCPU Structure
type NodeCPU struct {
	CPU  string `json:"cpu"`
	Node string `json:"node"`
}

// GetByRole returns all nodes with the specified role
func GetByRole(role string) ([]corev1.Node, error) {
	selector, err := labels.Parse(fmt.Sprintf("%s/%s=", testutils.LabelRole, role))
	if err != nil {
		return nil, err
	}
	return GetBySelector(selector)
}

// GetBySelector returns all nodes with the specified selector
func GetBySelector(selector labels.Selector) ([]corev1.Node, error) {
	nodes := &corev1.NodeList{}
	if err := testclient.Client.List(context.TODO(), nodes, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

// GetByLabels returns all nodes with the specified labels
func GetByLabels(nodeLabels map[string]string) ([]corev1.Node, error) {
	selector := labels.SelectorFromSet(nodeLabels)
	return GetBySelector(selector)
}

// GetByName returns a node object by for a node name
func GetByName(nodeName string) (*corev1.Node, error) {
	node := &corev1.Node{}
	key := types.NamespacedName{
		Name: nodeName,
	}
	if err := testclient.Client.Get(context.TODO(), key, node); err != nil {
		return nil, fmt.Errorf("failed to get node for the node %q", node.Name)
	}
	return node, nil
}

// GetNonPerformancesWorkers returns list of nodes with non matching perfomance profile labels
func GetNonPerformancesWorkers(nodeSelectorLabels map[string]string) ([]corev1.Node, error) {
	nonPerformanceWorkerNodes := []corev1.Node{}
	workerNodes, err := GetByRole(testutils.RoleWorker)
	for _, node := range workerNodes {
		for label := range nodeSelectorLabels {
			if _, ok := node.Labels[label]; !ok {
				nonPerformanceWorkerNodes = append(nonPerformanceWorkerNodes, node)
				break
			}
		}
	}
	return nonPerformanceWorkerNodes, err
}

// GetMachineConfigDaemonByNode returns the machine-config-daemon pod that runs on the specified node
func GetMachineConfigDaemonByNode(node *corev1.Node) (*corev1.Pod, error) {
	listOptions := &client.ListOptions{
		Namespace:     testutils.NamespaceMachineConfigOperator,
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set{"k8s-app": "machine-config-daemon"}),
	}

	mcds := &corev1.PodList{}
	if err := testclient.Client.List(context.TODO(), mcds, listOptions); err != nil {
		return nil, err
	}

	if len(mcds.Items) < 1 {
		return nil, fmt.Errorf("failed to get machine-config-daemon pod for the node %q", node.Name)
	}
	return &mcds.Items[0], nil
}

// ExecCommandOnMachineConfigDaemon returns the output of the command execution on the machine-config-daemon pod that runs on the specified node
func ExecCommandOnMachineConfigDaemon(node *corev1.Node, command []string) ([]byte, error) {
	mcd, err := GetMachineConfigDaemonByNode(node)
	if err != nil {
		return nil, err
	}
	testlog.Infof("found mcd %s for node %s", mcd.Name, node.Name)

	return testpods.WaitForPodOutput(testclient.K8sClient, mcd, command)
}

// ExecCommandOnNode executes given command on given node and returns the result
func ExecCommandOnNode(cmd []string, node *corev1.Node) (string, error) {
	out, err := ExecCommandOnMachineConfigDaemon(node, cmd)
	if err != nil {
		return "", err
	}

	trimmedString := strings.Trim(string(out), "\n")
	return strings.ReplaceAll(trimmedString, "\r", ""), nil
}

// GetKubeletConfig returns KubeletConfiguration loaded from the node /etc/kubernetes/kubelet.conf
func GetKubeletConfig(node *corev1.Node) (*kubeletconfigv1beta1.KubeletConfiguration, error) {
	command := []string{"cat", path.Join("/rootfs", testutils.FilePathKubeletConfig)}
	kubeletBytes, err := ExecCommandOnMachineConfigDaemon(node, command)
	if err != nil {
		return nil, err
	}

	testlog.Infof("command output: %s", string(kubeletBytes))
	kubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	if err := yaml.Unmarshal(kubeletBytes, kubeletConfig); err != nil {
		return nil, err
	}
	return kubeletConfig, err
}

// MatchingOptionalSelector filter the given slice with only the nodes matching the optional selector.
// If no selector is set, it returns the same list.
// The NODES_SELECTOR must be set with a labelselector expression.
// For example: NODES_SELECTOR="sctp=true"
// Inspired from: https://github.com/fedepaol/sriov-network-operator/blob/master/test/util/nodes/nodes.go
func MatchingOptionalSelector(toFilter []corev1.Node) ([]corev1.Node, error) {
	if testutils.NodesSelector == "" {
		return toFilter, nil
	}

	selector, err := labels.Parse(testutils.NodesSelector)
	if err != nil {
		return nil, fmt.Errorf("Error parsing the %s label selector, %v", testutils.NodesSelector, err)
	}

	toMatch, err := GetBySelector(selector)
	if err != nil {
		return nil, fmt.Errorf("Error in getting nodes matching the %s label selector, %v", testutils.NodesSelector, err)
	}
	if len(toMatch) == 0 {
		return nil, fmt.Errorf("Failed to get nodes matching %s label selector", testutils.NodesSelector)
	}

	res := make([]corev1.Node, 0)
	for _, n := range toFilter {
		for _, m := range toMatch {
			if n.Name == m.Name {
				res = append(res, n)
				break
			}
		}
	}

	return res, nil
}

// HasPreemptRTKernel returns no error if the node booted with PREEMPT RT kernel
func HasPreemptRTKernel(node *corev1.Node) error {
	// verify that the kernel-rt-core installed it also means the the machine booted with the RT kernel
	// because the machine-config-daemon uninstalls regular kernel once you install the RT one and
	// on traditional yum systems, rpm -q kernel can be completely different from what you're booted
	// because yum keeps multiple kernels but only one userspace;
	// with rpm-ostree rpm -q is telling you what you're booted into always,
	// because ostree binds together (kernel, userspace) as a single commit.
	cmd := []string{"chroot", "/rootfs", "rpm", "-q", "kernel-rt-core"}
	if _, err := ExecCommandOnNode(cmd, node); err != nil {
		return err
	}

	cmd = []string{"/bin/bash", "-c", "cat /rootfs/sys/kernel/realtime"}
	out, err := ExecCommandOnNode(cmd, node)
	if err != nil {
		return err
	}

	if out != "1" {
		return fmt.Errorf("RT kernel disabled")
	}

	return nil
}

func BannedCPUs(node corev1.Node) (banned cpuset.CPUSet, err error) {
	cmd := []string{"sed", "-n", "s/^IRQBALANCE_BANNED_CPUS=\\(.*\\)/\\1/p", "/rootfs/etc/sysconfig/irqbalance"}
	bannedCPUs, err := ExecCommandOnNode(cmd, &node)
	if err != nil {
		return cpuset.NewCPUSet(), fmt.Errorf("failed to execute %v: %v", cmd, err)
	}

	if bannedCPUs == "" {
		testlog.Infof("Banned CPUs on node %q returned empty set", node.Name)
		return cpuset.NewCPUSet(), nil // TODO: should this be a error?
	}

	banned, err = components.CPUMaskToCPUSet(bannedCPUs)
	if err != nil {
		return cpuset.NewCPUSet(), fmt.Errorf("failed to parse the banned CPUs: %v", err)
	}

	return banned, nil
}

// GetDefaultSmpAffinitySet returns the default smp affinity mask for the node
func GetDefaultSmpAffinitySet(node *corev1.Node) (cpuset.CPUSet, error) {
	command := []string{"cat", "/proc/irq/default_smp_affinity"}
	defaultSmpAffinity, err := ExecCommandOnNode(command, node)
	if err != nil {
		return cpuset.NewCPUSet(), err
	}
	return components.CPUMaskToCPUSet(defaultSmpAffinity)
}

// GetOnlineCPUsSet returns the list of online (being scheduled) CPUs on the node
func GetOnlineCPUsSet(node *corev1.Node) (cpuset.CPUSet, error) {
	command := []string{"cat", sysDevicesOnlineCPUs}
	onlineCPUs, err := ExecCommandOnNode(command, node)
	if err != nil {
		return cpuset.NewCPUSet(), err
	}
	return cpuset.Parse(onlineCPUs)
}

// GetSMTLevel returns the SMT level on the node using the given cpuID as target
// Use a random cpuID from the return value of GetOnlineCPUsSet if not sure
func GetSMTLevel(cpuID int, node *corev1.Node) int {
	cmd := []string{"/bin/sh", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/topology/thread_siblings_list | tr -d \"\n\r\"", cpuID)}
	threadSiblingsList, err := ExecCommandOnNode(cmd, node)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	// how many thread sibling you have = SMT level
	// example: 2-way SMT means 2 threads sibling for each thread
	cpus, err := cpuset.Parse(strings.TrimSpace(string(threadSiblingsList)))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return cpus.Size()
}

// GetNumaNodes returns the number of numa nodes and the associated cpus as list on the node
func GetNumaNodes(node *corev1.Node) (map[int][]int, error) {
	lscpuCmd := []string{"lscpu", "-e=cpu,node", "-J"}
	cmdout, err := ExecCommandOnNode(lscpuCmd, node)
	var numaNode, cpu int
	if err != nil {
		return nil, err
	}
	numaCpus := make(map[int][]int)
	var result NumaNodes
	err = json.Unmarshal([]byte(cmdout), &result)
	if err != nil {
		return nil, err
	}
	for _, value := range result.Cpus {
		if numaNode, err = strconv.Atoi(value.Node); err != nil {
			break
		}
		if cpu, err = strconv.Atoi(value.CPU); err != nil {
			break
		}
		numaCpus[numaNode] = append(numaCpus[numaNode], cpu)
	}
	return numaCpus, err
}

//TunedForNode find tuned pod for appropriate node
func TunedForNode(node *corev1.Node, sno bool) *corev1.Pod {

	listOptions := &client.ListOptions{
		Namespace:     components.NamespaceNodeTuningOperator,
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}),
	}

	tunedList := &corev1.PodList{}
	Eventually(func() bool {
		if err := testclient.Client.List(context.TODO(), tunedList, listOptions); err != nil {
			return false
		}

		if len(tunedList.Items) == 0 {
			return false
		}
		for _, s := range tunedList.Items[0].Status.ContainerStatuses {
			if s.Ready == false {
				return false
			}
		}
		return true

	}, cluster.ComputeTestTimeout(testTimeout*time.Second, sno), testPollInterval*time.Second).Should(BeTrue(),
		"there should be one tuned daemon per node")

	return &tunedList.Items[0]
}

func GetByCpuAllocatable(nodesList []corev1.Node, cpuQty int) []corev1.Node {
	nodesWithSufficientCpu := []corev1.Node{}
	for _, node := range nodesList {
		allocatableCPU, _ := node.Status.Allocatable.Cpu().AsInt64()
		if allocatableCPU >= int64(cpuQty) {
			nodesWithSufficientCpu = append(nodesWithSufficientCpu, node)
		}
	}
	return nodesWithSufficientCpu
}
