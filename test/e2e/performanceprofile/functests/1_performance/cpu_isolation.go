package __performance

import (
	"context"
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
//	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"

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
//			var testpod *corev1.Pod
//			var profile, initialProfile *performancev2.PerformanceProfile

			BeforeEach(func() {

				workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				Expect(workerRTNodes).ToNot(BeEmpty())
				workerRTNode = &workerRTNodes[0]

//				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
//				Expect(err).ToNot(HaveOccurred())

				//create and update wrapper
				wrapperFound, originalWrapper = createWrapper(workerRTNode, RUNC_WRAPPER_PATH)
				fmt.Println(wrapperFound, originalWrapper)

				originalConfig = updateRuncRuntimePath(workerRTNode, RUNC_WRAPPER_PATH, RUNTIME_CONFIG_FILE_PATH)
				fmt.Println("\n\n\n ORIGINAL CONFIG => \n\n",originalConfig)

				//create one guaranteed pod
				guaranteedPod = getGuaranteedPod(workerRTNode)
				//fmt.Print(*guaranteedPod)
			})

			AfterEach(func() {
				var cmd []string
				// delete/restore original wrapper the wrapper
				if wrapperFound {
					cmd = []string{"echo", "\"", originalWrapper, "\"", ">", RUNC_WRAPPER_PATH}
					_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred(), "Failed to restore %s , original wrapper : %s", RUNC_WRAPPER_PATH, originalWrapper)
				} else {
					cmd = []string{"rm", "-f", RUNC_WRAPPER_PATH}
					_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred(), "Failed to delete %s", RUNC_WRAPPER_PATH)
				}
				//undo the changes in runtime config file
				cmd = []string{"/bin/bash","-c",fmt.Sprintf("echo '%s' > /rootfs/etc/crio/crio.conf.d/99-runtimes.conf",originalConfig)}
				//cmd = []string{"echo", "' %s originalConfig, "\'", ">", RUNTIME_CONFIG_FILE_PATH}
				_, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				//Expect(err).ToNot(HaveOccurred(), "Failed to restore %s , original content : %s", RUNTIME_CONFIG_FILE_PATH, originalWrapper)

				//terminate guaranteed pod
				deletePod(guaranteedPod)

			})

			It("[test_id:9999] Verifies that runc excludes the cpus used by guaranteed pod", func() {

				secondPod := getNonGuaranteedPod(workerRTNode)
				//fmt.Print(secondPod)
				defer deletePod(secondPod)

				//TODO get the cpulist that runc is running on
				cmd := []string{"cat","/rootfs/var/roothome/create"}
				out, _ := nodes.ExecCommandOnNode(cmd, workerRTNode)
				pattern := `"cpus":\s+"([^"]+)"`
				re := regexp.MustCompile(pattern)
				matches := re.FindStringSubmatch(out)

				//fmt.Print("\n\n\n",out,"\n\n\n")
				fmt.Print("\n\n\n",matches,"\n\n\n")

				containerID, err := pods.GetContainerIDByName(guaranteedPod, "test")
				Expect(err).ToNot(HaveOccurred(), "unable to fetch containerId")

				containerCgroup := ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred(), "failed to execute %v", cmd)
					return containerCgroup
				})
				fmt.Print("\n\n",containerCgroup,"\n\n")

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
	//if found , save the original script to restore it after the test , create it if not
	cmd := []string{"cat", filePath}
	originalWrapper, err := nodes.ExecCommandOnNode(cmd, node)
	wrapperFound := false
	if err != nil {
		fmt.Println("Wrapper Not Found")

		cmd = []string{"/bin/bash","-c","echo '#!/bin/bash\nif [ -n \"$3\" ] && [ \"$3\" == \"create\" ] && [ -f \"$5/config.json\" ]; then\n        conf=\"$5/config.json\"\n        cat $conf >> /root/create\n\nfi\nexec /bin/runc \"$@\"' > /usr/local/bin/runc-wrapper.sh"}
		createdWrapper, err := nodes.ExecCommandOnNode(cmd, node)
		//Expect(err).ToNot(HaveOccurred(), "Failed to write to %s: %w", filePath, err)
		//Returns timed out error everytime it writes to the file
		fmt.Println("First time created wrapper")

	    cmd = []string{"cat", "/usr/local/bin/runc-wrapper.sh"}
		createdWrapper, err = nodes.ExecCommandOnNode(cmd, node)
		Expect(err).ToNot(HaveOccurred(), "Failed to write to %s: %w", filePath, err)
//		fmt.Print(createdWrapper, "\n\n")

		cmd = []string{"ls", "-l", "/usr/local/bin/"}
		createdWrapper, err = nodes.ExecCommandOnNode(cmd, node)
		fmt.Print(createdWrapper)
		return wrapperFound, createdWrapper
	} else {
		fmt.Println("Wrapper Found")
		wrapperFound = true
		return wrapperFound, originalWrapper
	}
}

func updateRuncRuntimePath(node *corev1.Node, filePath string, configPath string) string {
	//update the runtime_path inside configPath
	//newRuntimePath := "/usr/local/bin/runc-wrapper.sh"
	cmd := []string{"cat", configPath}
	originalFileContent, err := nodes.ExecCommandOnNode(cmd, node)
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
	//Expect(err).ToNot(HaveOccurred(), "Failed to update : %w", configPath, err)
	fmt.Print(err)

	cmd = []string{"cat",configPath}
	out, err := nodes.ExecCommandOnNode(cmd, node)
	fmt.Print("\n\n UPDATED FILE",out,"\n\n")

	return originalFileContent
}
