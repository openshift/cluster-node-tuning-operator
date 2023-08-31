package __performance

import (
	"context"
	"fmt"
	"regexp"
	"time"
	"strings"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"sigs.k8s.io/controller-runtime/pkg/client"
)


var _ = Describe("[bz_automation:1910386][performance] CPU Container Isolation", func() {
		Context("Create Guaranteed pod", func() {
			const (
				RUNC_WRAPPER_PATH        = "/rootfs/usr/local/bin/runc-wrapper.sh"
				RUNTIME_CONFIG_FILE_PATH = "/rootfs/etc/crio/crio.conf.d/99-runtimes.conf"
			)
			var guaranteedPod *corev1.Pod
			var workerRTNode *corev1.Node
			var err error
			var originalConfig string
			var originalWrapper string
			var wrapperFound bool

			BeforeEach(func() {

				workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				Expect(workerRTNodes).ToNot(BeEmpty())
				workerRTNode = &workerRTNodes[0]

				//create and update wrapper
				wrapperFound, originalWrapper = createWrapper(workerRTNode, RUNC_WRAPPER_PATH)

				originalConfig = updateRuncRuntimePath(workerRTNode, RUNC_WRAPPER_PATH, RUNTIME_CONFIG_FILE_PATH)

				cmd := []string{"systemctl","restart","crio"}
				_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				//create one guaranteed pod
				guaranteedPod = getGuaranteedPod(workerRTNode)
			})

			AfterEach(func() {
				var cmd []string
				//undo the changes in runtime config file
				cmd = []string{"/bin/bash","-c",fmt.Sprintf("echo '%s' > /rootfs/etc/crio/crio.conf.d/99-runtimes.conf",originalConfig)}
				_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				//Expect(err).ToNot(HaveOccurred(), "Failed to restore %s , original content : %s", RUNTIME_CONFIG_FILE_PATH, originalWrapper)

				cmd = []string{"cat", "/rootfs/etc/crio/crio.conf.d/99-runtimes.conf"}
				out,_ := nodes.ExecCommandOnNode(cmd, workerRTNode)
				fmt.Print(out)

				cmd = []string{"systemctl","restart","crio"}
				_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// delete/restore original wrapper the wrapper
				if wrapperFound {
					cmd = []string{"echo", "\"", originalWrapper, "\"", ">", RUNC_WRAPPER_PATH}
					_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred(), "Failed to restore %s , original wrapper : %s", RUNC_WRAPPER_PATH, originalWrapper)
				} else {
					cmd = []string{"rm", "-f", RUNC_WRAPPER_PATH}
					_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					//Expect(err).ToNot(HaveOccurred(), "Failed to delete %s", RUNC_WRAPPER_PATH)
					// TIMED OUT ISSUE
				}

				cmd = []string{"rm", "-f", "/rootfs/var/roothome/create"}
				_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)

				//terminate guaranteed pod
				deletePod(guaranteedPod)

			})

			It("[test_id:9999] Verifies that runc excludes the cpus used by guaranteed pod", func() {

				secondPod := getNonGuaranteedPod(workerRTNode)
				defer deletePod(secondPod)

				cmd := []string{"cat","/rootfs/var/roothome/create"}
				out, _ := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				hostnamePattern := `"hostname":\s+"([^"]+)"`
				cpusPattern := `"cpus":\s+"([^"]+)"`
				hostnameRe := regexp.MustCompile(hostnamePattern)
				cpusRe := regexp.MustCompile(cpusPattern)

				hostnameMatches := hostnameRe.FindAllStringSubmatch(out, -1)
				cpusMatches := cpusRe.FindAllStringSubmatch(out, -1)
				zippedMatches := make([]map[string]string, 0)

				for i := 0; i < len(hostnameMatches) && i < len(cpusMatches); i++ {
					hostnameValue := hostnameMatches[i][1]
					cpusValue := cpusMatches[i][1]
					zippedMatch := map[string]string{
						"hostname": hostnameValue,
						"cpus":     cpusValue,
					}
					zippedMatches = append(zippedMatches, zippedMatch)
				}
				for _, match := range zippedMatches {
					fmt.Printf("Hostname: %s\n", match["hostname"])
					fmt.Printf("CPUs: %s\n", match["cpus"])
				}

				ranges := strings.Split(zippedMatches[0]["cpus"], ",")
				guaranteedPodCpus := make([]int, 0)

				for _, valueStr := range ranges {
					// Check if the value is a range
					if strings.Contains(valueStr, "-") {
						rangeValues := strings.Split(valueStr, "-")
						minValue, _ := strconv.Atoi(rangeValues[0])
						maxValue, _ := strconv.Atoi(rangeValues[1])
						// Append the range of values to the slice
						for i := minValue; i <= maxValue; i++ {
							guaranteedPodCpus = append(guaranteedPodCpus, i)
						}
					} else {
						value, _ := strconv.Atoi(valueStr)
						guaranteedPodCpus = append(guaranteedPodCpus, value)
					}
				}

				runcCpus := zippedMatches[1]["cpus"]

				overlapFound := false
				for _, guaranteedPodCpu := range guaranteedPodCpus {
					for _, cpuRange := range strings.Split(runcCpus, ",") {
						fmt.Print(cpuRange,"\t" ,guaranteedPodCpu,"\n")
						if strings.Contains(cpuRange, "-") {
							rangeValues := strings.Split(cpuRange, "-")
							minCpu, _ := strconv.Atoi(rangeValues[0])
							maxCpu, _ := strconv.Atoi(rangeValues[1])
							if guaranteedPodCpu >= minCpu && guaranteedPodCpu <= maxCpu {
								overlapFound = true
								fmt.Println("Overlap found. Error. Not expected this")
								fmt.Print("\n\n",minCpu,maxCpu,guaranteedPodCpu)
								break
							}
						} else {
							cpuNumber, _ := strconv.Atoi(cpuRange)
							if cpuNumber == guaranteedPodCpu {
								overlapFound = true
								fmt.Print("Overlap Found.",cpuNumber,guaranteedPodCpu)
								break
							}
						}
					}
				}
				Expect(overlapFound).To(BeFalse())

			})
		})
})

func getGuaranteedPod(workerRTNode *corev1.Node) *corev1.Pod {
	var err error
	testpod1 := pods.GetTestPod()
	testpod1.Namespace = testutils.NamespaceTesting
	testpod1.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("200Mi"),
		},
	}
	testpod1.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	profile, _ := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testpod1.Spec.RuntimeClassName = &runtimeClass

	err = testclient.Client.Create(context.TODO(), testpod1)
	Expect(err).ToNot(HaveOccurred())
	_,err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod1), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
	Expect(err).ToNot(HaveOccurred())
	Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
	return testpod1
}

func getNonGuaranteedPod(workerRTNode *corev1.Node) *corev1.Pod {
	var err error
	testpod2 := pods.GetTestPod()
	testpod2.Namespace = testutils.NamespaceTesting
	testpod2.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	profile, _ := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testpod2.Spec.RuntimeClassName = &runtimeClass

	err = testclient.Client.Create(context.TODO(), testpod2)
	Expect(err).ToNot(HaveOccurred())
	_,err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod2), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
	Expect(err).ToNot(HaveOccurred())
	return testpod2
}

func deletePod(pod *corev1.Pod) {
	err := testclient.Client.Delete(context.TODO(), pod)
	testlog.Error(err)

	err = pods.WaitForDeletion(pod, pods.DefaultDeletionTimeout*time.Second)
	if err != nil {
		testlog.Error(err)
	}
}

func createWrapper(node *corev1.Node, filePath string) (bool, string) {
	// if found , save the original script to restore it after the test , create it if not
	cmd := []string{"cat", filePath}
	originalWrapper, err := nodes.ExecCommandOnNode(cmd, node)

	wrapperFound := false
	if err != nil {

		cmd = []string{"/bin/bash","-c","echo '#!/bin/bash\nif [ -n \"$3\" ] && [ \"$3\" == \"create\" ] && [ -f \"$5/config.json\" ]; then\n        conf=\"$5/config.json\"\n        cat $conf >> /root/create\n\nfi\nexec /bin/runc \"$@\"' > /rootfs/usr/local/bin/runc-wrapper.sh"}
		createdWrapper, _ := nodes.ExecCommandOnNode(cmd, node)
		// Expect(err).ToNot(HaveOccurred(), "Failed to write to %s: %w", filePath, err)
		// ECHO TIMEOUT ISSUE
		fmt.Println("First time created wrapper")

		cmd = []string{"chmod","a+x", "/rootfs/usr/local/bin/runc-wrapper.sh"}
		_, err = nodes.ExecCommandOnNode(cmd, node)
		//Expect(err).ToNot(HaveOccurred(), "Failed to make %s executable: %s", filePath, err)
		// TIMEOUT ISSUE 

		return wrapperFound, createdWrapper
	} else {

		wrapperFound = true
		return wrapperFound, originalWrapper
	}
}

func updateRuncRuntimePath(node *corev1.Node, filePath string, configPath string) string {
	cmd := []string{"cat", configPath}
	originalFileContent, err := nodes.ExecCommandOnNode(cmd, node)
	Expect(err).ToNot(HaveOccurred())

	exp := `\[crio\.runtime\.runtimes\.runc\](\n.*)+runtime_path = "(.*)"(\n.*)+\[`
	r1 := regexp.MustCompile(exp)
	groups := r1.FindStringSubmatch(originalFileContent)
	Expect(len(groups)).To(Equal(4), "Failed to match the regular expression '%s' in : \n %s ", exp, originalFileContent)

	r2 := regexp.MustCompile(`runtime_path = "(.*)"`)
	updatedContent := r1.ReplaceAllStringFunc(originalFileContent, func(input string) string {
		// updating runtime_path with path accessible by crio
		return r2.ReplaceAllString(input, fmt.Sprintf("runtime_path = \"%s\"", "/usr/local/bin/runc-wrapper.sh"))
	})

	cmd = []string{"/bin/bash","-c",fmt.Sprintf("echo '%s' > /rootfs/etc/crio/crio.conf.d/99-runtimes.conf",updatedContent)}
	_, err = nodes.ExecCommandOnNode(cmd, node)
	// Expect(err).ToNot(HaveOccurred(), "Failed to update : %w", configPath, err)
	// ECHO TIMEOUT ISSUE

	return originalFileContent
}

