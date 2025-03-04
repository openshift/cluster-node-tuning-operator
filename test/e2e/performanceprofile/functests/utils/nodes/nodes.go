package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/gomega"

	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	nodeInspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
)

const (
	testTimeout      = 480
	testPollInterval = 2
)

const (
	sysDevicesOnlineCPUs = "/sys/devices/system/cpu/online"
	cgroupRoot           = "/rootfs/sys/fs/cgroup"
)

// NumaNodes defines cpus in each numa node
type NumaNodes struct {
	Cpus []NodeCPU `json:"cpus"`
}

// NodeCPU Structure
type NodeCPU struct {
	Node string `json:"node"`
	Core string `json:"core"`
	CPU  string `json:"cpu"`
}

// Node Ether/virtual Interface
type NodeInterface struct {
	Name     string
	Physical bool
	UP       bool
	Bridge   bool
	defRoute bool
}

type ContainerInfo struct {
	PID int `json:"pid"`
}

type CrictlInfo struct {
	Info struct {
		Pid   int `json:"pid"`
		Linux struct {
			Resources struct {
				CPU struct {
					Shares int    `json:"shares"`
					Quota  int    `json:"quota"`
					Period int    `json:"period"`
					Cpus   string `json:"cpus"`
				} `json:"cpus"`
			} `json:"resources"`
			CgroupsPath string `json:"cgroupsPath"`
		} `json:"linux"`
	} `json:"info"`
}

type CpuManagerStateInfo struct {
	PolicyName    string `json:"policyName"`
	DefaultCPUSet string `json:"defaultCpuSet"`
	Checksum      int    `json:"checksum"`
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
	if err := testclient.DataPlaneClient.List(context.TODO(), nodes, &client.ListOptions{LabelSelector: selector}); err != nil {
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
	if err := testclient.DataPlaneClient.Get(context.TODO(), key, node); err != nil {
		return nil, fmt.Errorf("failed to get node for the node %q", nodeName)
	}
	return node, nil
}

// GetNonPerformancesWorkers returns list of nodes with non matching performance profile labels
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

// ExecCommand returns the output of the command execution as a []byte
func ExecCommand(ctx context.Context, node *corev1.Node, command []string) ([]byte, error) {
	return nodeInspector.ExecCommand(ctx, node, command)
}

// GetKubeletConfig returns KubeletConfiguration loaded from the node /etc/kubernetes/kubelet.conf
func GetKubeletConfig(ctx context.Context, node *corev1.Node) (*kubeletconfigv1beta1.KubeletConfiguration, error) {
	command := []string{"cat", path.Join("/rootfs", testutils.FilePathKubeletConfig)}
	kubeletBytes, err := ExecCommand(ctx, node, command)
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
func HasPreemptRTKernel(ctx context.Context, node *corev1.Node) error {
	// verify that the kernel-rt-core installed it also means the the machine booted with the RT kernel
	// because the machine-config-daemon uninstalls regular kernel once you install the RT one and
	// on traditional yum systems, rpm -q kernel can be completely different from what you're booted
	// because yum keeps multiple kernels but only one userspace;
	// with rpm-ostree rpm -q is telling you what you're booted into always,
	// because ostree binds together (kernel, userspace) as a single commit.
	cmd := []string{"chroot", "/rootfs", "rpm", "-q", "kernel-rt-core"}
	if _, err := ExecCommand(ctx, node, cmd); err != nil {
		return err
	}

	cmd = []string{"/bin/bash", "-c", "cat /rootfs/sys/kernel/realtime"}
	output, err := ExecCommand(ctx, node, cmd)
	if err != nil {
		return err
	}
	out := testutils.ToString(output)

	if out != "1" {
		return fmt.Errorf("RT kernel disabled")
	}

	return nil
}

func GetDefaultSmpAffinityRaw(ctx context.Context, node *corev1.Node) (string, error) {
	cmd := []string{"cat", "/proc/irq/default_smp_affinity"}
	out, err := ExecCommand(ctx, node, cmd)
	if err != nil {
		return "", err
	}
	output := testutils.ToString(out)
	return output, nil
}

// GetDefaultSmpAffinitySet returns the default smp affinity mask for the node
// Warning: Please note that default smp affinity mask is not aware
//
//	of offline cpus and will return the affinity bits for those
//	as well. You must intersect the mask with the mask returned
//	by GetOnlineCPUsSet if this is not desired.
func GetDefaultSmpAffinitySet(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	defaultSmpAffinity, err := GetDefaultSmpAffinityRaw(ctx, node)
	if err != nil {
		return cpuset.New(), err
	}
	return components.CPUMaskToCPUSet(defaultSmpAffinity)
}

// GetOnlineCPUsSet returns the list of online (being scheduled) CPUs on the node
func GetOnlineCPUsSet(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	command := []string{"cat", sysDevicesOnlineCPUs}
	out, err := ExecCommand(ctx, node, command)
	if err != nil {
		return cpuset.New(), err
	}
	onlineCPUs := testutils.ToString(out)
	return cpuset.Parse(onlineCPUs)
}

// GetSMTLevel returns the SMT level on the node using the given cpuID as target
// Use a random cpuID from the return value of GetOnlineCPUsSet if not sure
func GetSMTLevel(ctx context.Context, cpuID int, node *corev1.Node) int {
	cmd := []string{"/bin/sh", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/topology/thread_siblings_list | tr -d \"\n\r\"", cpuID)}
	out, err := ExecCommand(ctx, node, cmd)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	threadSiblingsList := testutils.ToString(out)
	// how many thread sibling you have = SMT level
	// example: 2-way SMT means 2 threads sibling for each thread
	cpus, err := cpuset.Parse(strings.TrimSpace(threadSiblingsList))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return cpus.Size()
}

// GetNumaNodes returns the number of numa nodes and the associated cpus as list on the node
func GetNumaNodes(ctx context.Context, node *corev1.Node) (map[int][]int, error) {
	lscpuCmd := []string{"lscpu", "-e=node,core,cpu", "-J"}
	out, err := ExecCommand(ctx, node, lscpuCmd)
	var numaNode, cpu int
	if err != nil {
		return nil, err
	}
	cmdout := testutils.ToString(out)
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

// GetCoreSiblings returns the siblings of core per numa node
func GetCoreSiblings(ctx context.Context, node *corev1.Node) (map[int]map[int][]int, error) {
	coreSiblings := make(map[int]map[int][]int)
	lscpuCmd := []string{"lscpu", "-e=node,core,cpu", "-J"}
	output, err := ExecCommand(ctx, node, lscpuCmd)
	if err != nil {
		return coreSiblings, err
	}
	out := testutils.ToString(output)
	var result NumaNodes
	var numaNode, core, cpu int
	err = json.Unmarshal([]byte(out), &result)
	if err != nil {
		return nil, err
	}
	for _, value := range result.Cpus {
		if numaNode, err = strconv.Atoi(value.Node); err != nil {
			break
		}
		if core, err = strconv.Atoi(value.Core); err != nil {
			break
		}
		if cpu, err = strconv.Atoi(value.CPU); err != nil {
			break
		}
		if coreSiblings[numaNode] == nil {
			coreSiblings[numaNode] = make(map[int][]int)
		}
		coreSiblings[numaNode][core] = append(coreSiblings[numaNode][core], cpu)
	}
	return coreSiblings, err
}

// TunedForNode find tuned pod for appropriate node
func TunedForNode(node *corev1.Node, sno bool) *corev1.Pod {
	listOptions := &client.ListOptions{
		Namespace:     components.NamespaceNodeTuningOperator,
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}),
	}

	tunedList := &corev1.PodList{}
	Eventually(func() bool {
		if err := testclient.DataPlaneClient.List(context.TODO(), tunedList, listOptions); err != nil {
			return false
		}

		if len(tunedList.Items) == 0 {
			return false
		}
		for _, s := range tunedList.Items[0].Status.ContainerStatuses {
			if !s.Ready {
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

func GetByCpuCapacity(nodesList []corev1.Node, cpuQty int) []corev1.Node {
	nodesWithSufficientCpu := []corev1.Node{}
	for _, node := range nodesList {
		capacityCPU, _ := node.Status.Capacity.Cpu().AsInt64()
		if capacityCPU >= int64(cpuQty) {
			nodesWithSufficientCpu = append(nodesWithSufficientCpu, node)
		}
	}
	return nodesWithSufficientCpu
}

// GetAndRemoveCpuSiblingsFromMap function returns the cpus siblings associated with core
// Also updates the map by deleting the cpu siblings returned
func GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings map[int]map[int][]int, coreId int) []string {
	var cpuSiblings []string
	// Iterate over the  Numa node in the map
	for node := range numaCoreSiblings {
		// Check if the coreId exists in the Numa node
		_, ok := numaCoreSiblings[node][coreId]
		if ok {
			// Iterate over the siblings of the coreId
			for _, sibling := range numaCoreSiblings[node][coreId] {
				cpuSiblings = append(cpuSiblings, strconv.Itoa(sibling))
			}
			// Delete the cpusiblings of that particular coreid
			delete(numaCoreSiblings[node], coreId)
		}
	}
	return cpuSiblings
}

// GetNumaRanges function Splits the numa Siblings in to multiple Ranges
// Example for Cpu Siblings:  10,50,11,51,12,52,13,53,14,54 , will return 10-14,50-54
func GetNumaRanges(cpuString string) string {
	cpuList := strings.Split(cpuString, ",")
	var cpuIds = []int{}
	for _, v := range cpuList {
		cpuId, _ := strconv.Atoi(v)
		cpuIds = append(cpuIds, cpuId)
	}
	sort.Ints(cpuIds)
	offlineCpuRanges := []string{}
	var j, k int
	for i := 0; i < len(cpuIds); i++ {
		j = i + 1
		if j < len(cpuIds) {
			if (cpuIds[i] + 1) != cpuIds[j] {
				r := make([]int, 0)
				for ; k < j; k++ {
					r = append(r, cpuIds[k])
				}
				k = j
				offlineCpuRanges = append(offlineCpuRanges, fmt.Sprintf("%d-%d", r[0], r[len(r)-1]))
			}
		}
	}
	//left overs
	for i := k; i < len(cpuIds); i++ {
		offlineCpuRanges = append(offlineCpuRanges, fmt.Sprintf("%d", cpuIds[i]))
	}
	return strings.Join(offlineCpuRanges, ",")
}

// Get Node Ethernet/Virtual Interfaces
func GetNodeInterfaces(ctx context.Context, node corev1.Node) ([]NodeInterface, error) {
	var nodeInterfaces []NodeInterface
	listNetworkInterfacesCmd := []string{"/bin/sh", "-c", "ls -l /sys/class/net"}
	networkInterfaces, err := ExecCommand(ctx, &node, listNetworkInterfacesCmd)
	if err != nil {
		return nil, err
	}
	ipLinkShowCmd := []string{"ip", "link", "show"}
	interfaceLinksStatus, err := ExecCommand(ctx, &node, ipLinkShowCmd)
	if err != nil {
		return nil, err
	}
	defaultRouteCmd := []string{"ip", "route", "show", "0.0.0.0/0"}
	defaultRoute, err := ExecCommand(ctx, &node, defaultRouteCmd)
	if err != nil {
		return nil, err
	}

	for _, networkInterface := range strings.Split(string(networkInterfaces), "\n") {
		nodeInterface := new(NodeInterface)
		splitIface := strings.Split(networkInterface, "/")
		interfaceName := strings.ReplaceAll(splitIface[len(splitIface)-1], "\r", "")
		nodeInterface.Name = interfaceName
		if !strings.Contains(networkInterface, "virtual") {
			nodeInterface.Physical = true
		}
		if len(splitIface) > 1 {
			for _, interfaceInfo := range strings.Split(string(interfaceLinksStatus), "ff:ff:ff:ff:ff:ff") {
				strippedInterfaceInfo := strings.Split(interfaceInfo, "\n")[1]
				if strings.Contains(strippedInterfaceInfo, interfaceName) {
					if strings.Contains(strippedInterfaceInfo, "state UP") {
						nodeInterface.UP = true
					}
					if strings.Contains(strippedInterfaceInfo, "master") {
						nodeInterface.Bridge = true
					}
					if strings.Contains(string(defaultRoute), interfaceName) {
						nodeInterface.defRoute = true
					}
				}
			}
		}
		nodeInterfaces = append(nodeInterfaces, *nodeInterface)
	}
	return nodeInterfaces, err
}

func WaitForReadyOrFail(tag, nodeName string, timeout, polling time.Duration) {
	testlog.Infof("%s: waiting for node %q: to be ready", tag, nodeName)
	EventuallyWithOffset(1, func() (bool, error) {
		node, err := GetByName(nodeName)
		if err != nil {
			// intentionally tolerate error
			testlog.Infof("wait for node %q ready: %v", nodeName, err)
			return false, nil
		}
		ready := isNodeReady(*node)
		testlog.Infof("node %q ready=%v", nodeName, ready)
		return ready, nil
	}).WithTimeout(timeout).WithPolling(polling).Should(BeTrue(), "post reboot: cannot get readiness status after reboot for node %q", nodeName)
	testlog.Infof("%s: node %q: reported ready", tag, nodeName)
}

func isNodeReady(node corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ContainerPid returns container process pid using crictl inspect command
func ContainerPid(ctx context.Context, node *corev1.Node, containerId string) (string, error) {
	var err error
	var criInfo CrictlInfo
	var cridata = []byte{}
	Eventually(func() []byte {
		cmd := []string{"/usr/sbin/chroot", "/rootfs", "crictl", "inspect", containerId}
		cridata, err = ExecCommand(ctx, node, cmd)
		Expect(err).ToNot(HaveOccurred(), "failed to run %s cmd", cmd)
		return cridata
	}, 10*time.Second, 5*time.Second).ShouldNot(BeEmpty())
	err = json.Unmarshal(cridata, &criInfo)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(criInfo.Info.Pid), err
}

// CpuManagerCpuSet returns cpu manager state file data
func CpuManagerCpuSet(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	stateFilePath := "/var/lib/kubelet/cpu_manager_state"
	var stateData CpuManagerStateInfo
	cmd := []string{"/usr/sbin/chroot", "/rootfs", "cat", stateFilePath}
	data, err := ExecCommand(ctx, node, cmd)
	if err != nil {
		return cpuset.New(), err
	}
	err = json.Unmarshal(data, &stateData)
	if err != nil {
		return cpuset.New(), err
	}
	nodeCpuSet, err := cpuset.Parse(stateData.DefaultCPUSet)
	if err != nil {
		return cpuset.New(), err
	}
	fmt.Println("cpuset = ", nodeCpuSet.String())
	return nodeCpuSet, err
}

// GetL3SharedCPUs creates a function that retrieves cpus for a given Node
// takes a worker cnf node and returns a closure when called with cpuId returns
// the corresponding cpus of core complex to which cpuId is part of
func GetL3SharedCPUs(node *corev1.Node) func(cpuId int) (cpuset.CPUSet, error) {
	return func (cpuId int) (cpuset.CPUSet, error) {
		cacheSizeFile := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cache/index3/shared_cpu_list", cpuId)
		cmd := []string{"cat", cacheSizeFile}
		ctx := context.Background()
		output, err := ExecCommand(ctx, node, cmd)
		if err != nil {
			return cpuset.CPUSet{}, fmt.Errorf("Unable to fetch shared cpu list: %v", err)
		}
		cpuSet, err := cpuset.Parse(strings.TrimSpace(string(output)))
		return cpuSet, err
	}
}
