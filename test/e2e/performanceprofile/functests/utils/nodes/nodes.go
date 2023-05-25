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

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	testpods "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
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
	irqAff, err := GetDefaultSmpAffinityRaw(&node)
	if err != nil {
		return cpuset.NewCPUSet(), err
	}
	testlog.Infof("Default SMP IRQ affinity on node %q is {%s} expected mask length %d", node.Name, irqAff, len(irqAff))

	cmd := []string{"sed", "-n", "s/^IRQBALANCE_BANNED_CPUS=\\(.*\\)/\\1/p", "/rootfs/etc/sysconfig/irqbalance"}
	bannedCPUs, err := ExecCommandOnNode(cmd, &node)
	if err != nil {
		return cpuset.NewCPUSet(), fmt.Errorf("failed to execute %v: %v", cmd, err)
	}

	testlog.Infof("Banned CPUs on node %q raw value is {%s}", node.Name, bannedCPUs)

	unquotedBannedCPUs := unquote(bannedCPUs)

	if unquotedBannedCPUs == "" {
		testlog.Infof("Banned CPUs on node %q returned empty set", node.Name)
		return cpuset.NewCPUSet(), nil // TODO: should this be a error?
	}

	fixedBannedCPUs := fixMaskPadding(unquotedBannedCPUs, len(irqAff))
	testlog.Infof("Fixed Banned CPUs on node %q {%s}", node.Name, fixedBannedCPUs)

	banned, err = components.CPUMaskToCPUSet(fixedBannedCPUs)
	if err != nil {
		return cpuset.NewCPUSet(), fmt.Errorf("failed to parse the banned CPUs: %v", err)
	}

	return banned, nil
}

func unquote(s string) string {
	q := "\""
	s = strings.TrimPrefix(s, q)
	s = strings.TrimSuffix(s, q)
	return s
}

func fixMaskPadding(rawMask string, maskLen int) string {
	maskString := strings.ReplaceAll(rawMask, ",", "")

	fixedMask := fixMask(maskString, maskLen)
	testlog.Infof("fixed mask (dealing with incorrect crio padding) on node is {%s} len=%d", fixedMask, maskLen)

	retMask := fixedMask[0:8]
	for i := 8; i+8 <= len(fixedMask); i += 8 {
		retMask = retMask + "," + fixedMask[i:i+8]
	}
	return retMask
}

func fixMask(maskString string, maskLen int) string {
	if maskLen >= len(maskString) {
		return maskString
	}
	return strings.Repeat("0", len(maskString)-maskLen) + maskString[len(maskString)-maskLen:]
}

func GetDefaultSmpAffinityRaw(node *corev1.Node) (string, error) {
	cmd := []string{"cat", "/proc/irq/default_smp_affinity"}
	return ExecCommandOnNode(cmd, node)
}

// GetDefaultSmpAffinitySet returns the default smp affinity mask for the node
// Warning: Please note that default smp affinity mask is not aware
//
//	of offline cpus and will return the affinity bits for those
//	as well. You must intersect the mask with the mask returned
//	by GetOnlineCPUsSet if this is not desired.
func GetDefaultSmpAffinitySet(node *corev1.Node) (cpuset.CPUSet, error) {
	defaultSmpAffinity, err := GetDefaultSmpAffinityRaw(node)
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
	lscpuCmd := []string{"lscpu", "-e=node,core,cpu", "-J"}
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

// GetCoreSiblings returns the siblings of core per numa node
func GetCoreSiblings(node *corev1.Node) (map[int]map[int][]int, error) {
	lscpuCmd := []string{"lscpu", "-e=node,core,cpu", "-J"}
	out, err := ExecCommandOnNode(lscpuCmd, node)
	var result NumaNodes
	var numaNode, core, cpu int
	coreSiblings := make(map[int]map[int][]int)
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
func GetNodeInterfaces(node corev1.Node) ([]NodeInterface, error) {
	var nodeInterfaces []NodeInterface
	listNetworkInterfacesCmd := []string{"/bin/sh", "-c", fmt.Sprintf("ls -l /sys/class/net")}
	networkInterfaces, err := ExecCommandOnMachineConfigDaemon(&node, listNetworkInterfacesCmd)
	if err != nil {
		return nil, err
	}
	ipLinkShowCmd := []string{"ip", "link", "show"}
	interfaceLinksStatus, err := ExecCommandOnMachineConfigDaemon(&node, ipLinkShowCmd)
	if err != nil {
		return nil, err
	}
	defaultRouteCmd := []string{"ip", "route", "show", "0.0.0.0/0"}
	defaultRoute, err := ExecCommandOnMachineConfigDaemon(&node, defaultRouteCmd)
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
