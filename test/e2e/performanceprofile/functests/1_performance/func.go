package __performance

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

var workerRTNode *corev1.Node
var profile *performancev2.PerformanceProfile

const restartCooldownTime = 1 * time.Minute
const cgroupRoot string = "/sys/fs/cgroup"

type CPUVals struct {
	CPUs string `json:"cpus"`
}

type CPUResources struct {
	CPU CPUVals `json:"cpu"`
}

type LinuxResources struct {
	Resources CPUResources `json:"resources"`
}

type Process struct {
	Args []string `json:"args"`
}

type Annotations struct {
	ContainerName string `json:"io.kubernetes.container.name"`
	PodName       string `json:"io.kubernetes.pod.name"`
}

type ContainerConfig struct {
	Process     Process        `json:"process"`
	Hostname    string         `json:"hostname"`
	Annotations Annotations    `json:"annotations"`
	Linux       LinuxResources `json:"linux"`
}

func makePod(ctx context.Context, workerRTNode *corev1.Node, guaranteed bool) *corev1.Pod {
	testPod := pods.GetTestPod()
	testPod.Namespace = testutils.NamespaceTesting
	testPod.Spec.NodeName = workerRTNode.Name
	testPod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
	if guaranteed {
		testPod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("200Mi"),
			},
		}
	}
	profile, _ := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testPod.Spec.RuntimeClassName = &runtimeClass
	return testPod
}

func checkForWorkloadPartitioning(ctx context.Context) bool {
	// Look for the correct Workload Partition annotation in
	// a crio configuration file on the target node
	By("Check for Workload Partitioning enabled")
	cmd := []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		"echo CHECK ; /bin/grep -rEo 'activation_annotation.*target\\.workload\\.openshift\\.io/management.*' /etc/crio/crio.conf.d/ || true",
	}
	out, err := nodes.ExecCommand(ctx, workerRTNode, cmd)
	Expect(err).ToNot(HaveOccurred(), "Unable to check cluster for Workload Partitioning enabled")
	output := testutils.ToString(out)
	re := regexp.MustCompile(`activation_annotation.*target\.workload\.openshift\.io/management.*`)
	return re.MatchString(fmt.Sprint(output))
}

func checkPodHTSiblings(ctx context.Context, testpod *corev1.Pod) bool {
	By("Get test pod CPU list")
	containerID, err := pods.GetContainerIDByName(testpod, "test")
	Expect(err).ToNot(HaveOccurred(), "Unable to get pod containerID")

	cmd := []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("/bin/crictl inspect %s | /bin/jq -r '.info.runtimeSpec.linux.resources.cpu.cpus'", containerID),
	}
	node, err := nodes.GetByName(testpod.Spec.NodeName)
	Expect(err).ToNot(HaveOccurred(), "failed to get node %q", testpod.Spec.NodeName)
	Expect(testpod.Spec.NodeName).ToNot(BeEmpty(), "testpod %s/%s still pending - no nodeName set", testpod.Namespace, testpod.Name)
	out, err := nodes.ExecCommand(ctx, node, cmd)
	Expect(err).ToNot(HaveOccurred(), "Unable to crictl inspect containerID %q", containerID)
	output := testutils.ToString(out)
	podcpus, err := cpuset.Parse(strings.Trim(output, "\n"))
	Expect(err).ToNot(
		HaveOccurred(), "Unable to cpuset.Parse pod allocated cpu set from output %s", output)
	testlog.Infof("Test pod CPU list: %s", podcpus.String())

	// aggregate cpu sibling paris from the host based on the cpus allocated to the pod
	By("Get host cpu siblings for pod cpuset")
	hostHTSiblingPaths := strings.Builder{}
	for _, cpuNum := range podcpus.List() {
		_, err = hostHTSiblingPaths.WriteString(
			fmt.Sprintf(" /sys/devices/system/cpu/cpu%d/topology/thread_siblings_list", cpuNum),
		)
		Expect(err).ToNot(HaveOccurred(), "Build.Write failed to add dir path string?")
	}
	cmd = []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("/bin/cat %s | /bin/sort -u", hostHTSiblingPaths.String()),
	}
	out, err = nodes.ExecCommand(ctx, workerRTNode, cmd)
	Expect(err).ToNot(
		HaveOccurred(),
		"Unable to read host thread_siblings_list files",
	)
	output = testutils.ToString(out)

	// output is newline separated. Convert to cpulist format by replacing internal "\n" chars with ","
	hostHTSiblings := strings.ReplaceAll(
		strings.Trim(fmt.Sprint(output), "\n"), "\n", ",",
	)

	hostcpus, err := cpuset.Parse(hostHTSiblings)
	Expect(err).ToNot(HaveOccurred(), "Unable to parse host cpu HT siblings: %s", hostHTSiblings)
	By(fmt.Sprintf("Host CPU sibling set from querying for pod cpus: %s", hostcpus.String()))

	// pod cpu list should have the same siblings as the host for the same cpus
	return hostcpus.Equals(podcpus)
}

func startHTtestPod(ctx context.Context, cpuCount int) *corev1.Pod {
	var testpod *corev1.Pod

	annotations := map[string]string{}
	testpod = getTestPodWithAnnotations(annotations, cpuCount)
	testpod.Namespace = testutils.NamespaceTesting

	By(fmt.Sprintf("Creating test pod with %d cpus", cpuCount))
	testlog.Info(pods.DumpResourceRequirements(testpod))
	err := testclient.DataPlaneClient.Create(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())
	testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
	logEventsForPod(testpod)
	Expect(err).ToNot(HaveOccurred(), "Start pod failed")
	// Sanity check for QoS Class == Guaranteed
	Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed),
		"Test pod does not have QoS class of Guaranteed")
	return testpod
}

func isSMTAlignmentError(pod *corev1.Pod) bool {
	re := regexp.MustCompile(`SMT.*Alignment.*Error`)
	return re.MatchString(pod.Status.Reason)
}

func getStressPod(nodeName string, cpus int) *corev1.Pod {
	cpuCount := fmt.Sprintf("%d", cpus)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cpu-",
			Labels: map[string]string{
				"test": "",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "stress-test",
					Image: images.Test(),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpuCount),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					Command: []string{"/usr/bin/stresser"},
					Args:    []string{"-cpus", cpuCount},
				},
			},
			NodeSelector: map[string]string{
				testutils.LabelHostname: nodeName,
			},
		},
	}
}

func promotePodToGuaranteed(pod *corev1.Pod) *corev1.Pod {
	for idx := 0; idx < len(pod.Spec.Containers); idx++ {
		cnt := &pod.Spec.Containers[idx] // shortcut
		if cnt.Resources.Limits == nil {
			cnt.Resources.Limits = make(corev1.ResourceList)
		}
		for resName, resQty := range cnt.Resources.Requests {
			cnt.Resources.Limits[resName] = resQty
		}
	}
	return pod
}

func getTestPodWithProfileAndAnnotations(perfProf *performancev2.PerformanceProfile, annotations map[string]string, cpus int) *corev1.Pod {
	testpod := pods.GetTestPod()
	if len(annotations) > 0 {
		testpod.Annotations = annotations
	}
	testpod.Namespace = testutils.NamespaceTesting

	cpuCount := fmt.Sprintf("%d", cpus)

	resCpu := resource.MustParse(cpuCount)
	resMem := resource.MustParse("256Mi")

	// change pod resource requirements, to change the pod QoS class to guaranteed
	testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resCpu,
			corev1.ResourceMemory: resMem,
		},
	}

	if perfProf != nil {
		runtimeClassName := components.GetComponentName(perfProf.Name, components.ComponentNamePrefix)
		testpod.Spec.RuntimeClassName = &runtimeClassName
	}

	return testpod
}

func getTestPodWithAnnotations(annotations map[string]string, cpus int) *corev1.Pod {
	testpod := getTestPodWithProfileAndAnnotations(profile, annotations, cpus)

	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	return testpod
}

func deleteTestPod(ctx context.Context, testpod *corev1.Pod) (types.UID, bool) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return "", false
	}

	testpodUID := testpod.UID

	err = testclient.DataPlaneClient.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())

	return testpodUID, true
}

func cpuSpecToString(cpus *performancev2.CPU) (string, error) {
	if cpus == nil {
		return "", fmt.Errorf("performance CPU field is nil")
	}
	sb := strings.Builder{}
	if cpus.Reserved != nil {
		_, err := fmt.Fprintf(&sb, "reserved=[%s]", *cpus.Reserved)
		if err != nil {
			return "", err
		}
	}
	if cpus.Isolated != nil {
		_, err := fmt.Fprintf(&sb, " isolated=[%s]", *cpus.Isolated)
		if err != nil {
			return "", err
		}
	}
	if cpus.BalanceIsolated != nil {
		_, err := fmt.Fprintf(&sb, " balanceIsolated=%t", *cpus.BalanceIsolated)
		if err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

func logEventsForPod(testPod *corev1.Pod) {
	evs, err := events.GetEventsForObject(testclient.DataPlaneClient, testPod.Namespace, testPod.Name, string(testPod.UID))
	if err != nil {
		testlog.Error(err)
	}
	for _, event := range evs.Items {
		testlog.Warningf("-> %s %s %s", event.Action, event.Reason, event.Message)
	}
}

// getCPUswithLoadBalanceDisabled Return cpus which are not in any scheduling domain
func getCPUswithLoadBalanceDisabled(ctx context.Context, targetNode *corev1.Node) ([]string, error) {
	cmd := []string{"/bin/bash", "-c", "cat /proc/schedstat"}
	out, err := nodes.ExecCommand(ctx, targetNode, cmd)
	if err != nil {
		return nil, err
	}
	schedstatData := testutils.ToString(out)

	info, err := schedstat.ParseData(strings.NewReader(schedstatData))
	if err != nil {
		return nil, err
	}

	cpusWithoutDomain := []string{}
	for _, cpu := range info.GetCPUs() {
		doms, ok := info.GetDomains(cpu)
		if !ok {
			return nil, fmt.Errorf("unknown cpu: %v", cpu)
		}
		if len(doms) > 0 {
			continue
		}
		cpusWithoutDomain = append(cpusWithoutDomain, cpu)
	}

	return cpusWithoutDomain, nil
}

// getPodCpus return cpus used based on taskset
func getPodCpus(testpod *corev1.Pod) (string, error) {
	tasksetcmd := []string{"taskset", "-pc", "1"}
	testpodCpusByte, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, testpod.Spec.Containers[0].Name, tasksetcmd)
	if err != nil {
		return "", err
	}
	testpodCpusStr := string(testpodCpusByte)
	parts := strings.Split(strings.TrimSpace(testpodCpusStr), ":")
	cpus := strings.TrimSpace(parts[1])
	return cpus, err
}

// checkSchedulingDomains Check cpus are part of any scheduling domain
func checkSchedulingDomains(workerRTNode *corev1.Node, podCpus cpuset.CPUSet, testFunc func(cpuset.CPUSet) error, timeout, polling time.Duration, errMsg string) {
	Eventually(func() error {
		cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
		Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus with load balancing disabled from /proc/schedstat")
		cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
		if err != nil {
			return err
		}
		cpuIDs := cpuset.New(cpuIDList...)
		return testFunc(cpuIDs)
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(HaveOccurred(), errMsg)
}

// busyCpuImageEnv return busycpus image used for crio quota annotations test
// This is required for running tests on disconnected environment where images are mirrored
// in private registries.
func busyCpuImageEnv() string {
	qeImageRegistry := os.Getenv("IMAGE_REGISTRY")
	busyCpusImage := os.Getenv("BUSY_CPUS_IMAGE")

	if busyCpusImage == "" {
		busyCpusImage = "busycpus"
	}

	if qeImageRegistry == "" {
		qeImageRegistry = "quay.io/ocp-edge-qe/"
	}
	return fmt.Sprintf("%s%s", qeImageRegistry, busyCpusImage)
}

// isPodReady checks if the pod is in ready state
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
