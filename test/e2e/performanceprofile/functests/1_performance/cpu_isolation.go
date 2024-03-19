package __performance

import (
	"context"
	"fmt"
	"regexp"
	"time"
	"strings"
	"strconv"
	"encoding/json"
	"encoding/base64"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"sigs.k8s.io/controller-runtime/pkg/client"
	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	assets "github.com/openshift/cluster-node-tuning-operator/assets/performanceprofile"
)

const (
	criofixScript                = "runc-wrapper.sh"
	criofixPath                  = "scripts"
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
	defaultIgnitionVersion       = "3.2.0"
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
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred(), "Unable to gather profile")

				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred(), "Unable to gather performance MCP")

				mc, err := createMachineConfig(profile)
				Expect(err).ToNot(HaveOccurred(), "Unable to create machine config file")

				err = testclient.Client.Create(context.TODO(), mc)
				Expect(err).ToNot(HaveOccurred(), "Unable to apply the mc")

				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

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


func createMachineConfig(profile *performancev2.PerformanceProfile) (*machineconfigv1.MachineConfig, error){
	mcName := fmt.Sprintf("51-%s","crio-full-aff")
	mc := &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind: "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: mcName,
			Labels: profilecomponent.GetMachineConfigLabel(profile),
		},
		Spec: machineconfigv1.MachineConfigSpec{},
	}
	ignitionConfig, err := addWrapper()
	if err != nil{
		return nil, err
	}
	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil{
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return mc, nil
}

func addWrapper() (*igntypes.Config, error) {
	ignitionConfig := &igntypes.Config{
			Ignition: igntypes.Ignition{
					Version: defaultIgnitionVersion,
			},
			Storage: igntypes.Storage{
					Files: []igntypes.File{},
			},
	}

	scriptMode := 0700
	content, err := assets.Scripts.ReadFile(fmt.Sprintf("%s/%s", criofixPath, criofixScript))
	if err != nil {
			return nil, err
	}
	addContent(ignitionConfig, content, "/usr/local/bin/"+criofixScript, scriptMode)
	return ignitionConfig, nil
}

func addContent(ignitionConfig *igntypes.Config, content []byte, dst string, mode int) {
	contentBase64 := base64.StdEncoding.EncodeToString(content)
	ignitionConfig.Storage.Files = append(ignitionConfig.Storage.Files, igntypes.File{
			Node: igntypes.Node{
					Path: dst,
			},
			FileEmbedded1: igntypes.FileEmbedded1{
					Contents: igntypes.Resource{
							Source: pointer.StringPtr(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, contentBase64)),
					},
					Mode: &mode,
			},
	})
}