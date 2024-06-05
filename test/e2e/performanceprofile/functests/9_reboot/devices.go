package __reboot_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	testnodes "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	testpods "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
)

// This test has a very high disruption potential: it wants to bypass the node cordoning et. al.
// and kubernetes management and trigger a reboot straight from the node.
// Additionally, it explicitly considers SNO, where the disruption potential is even higher.
// Because of all the above, it must opted-in explicitly by supplying the `DEVICE_RECOVERY_TARGET_NODE`
// variable pointing to a node in the cluster, SNO or worker node.

const (
	rebootNodeCommandMCD     = "chroot /rootfs systemctl reboot"
	kubeletRestartCommandMCD = "chroot /rootfs systemctl restart kubelet"

	// note NO DEFAULT for targetNode - intentionally
	sriovDeviceResourceNameDefault = "openshift.io/dpdk_nic_1"
	workloadContainerImageDefault  = "quay.io/fromani/devaccess:2023060101" // FIXME

	targetNodeEnvVar              = "DEVICE_RECOVERY_TARGET_NODE"
	sriovDeviceResourceNameEnvVar = "DEVICE_RECOVERY_RESOURCE_NAME"
	workloadContainerImageEnvVar  = "DEVICE_RECOVERY_CONTAINER_IMAGE"

	// this is the only timeout tunable because is a hard sleep. Every other long timeout is within a polling loop.
	rebootCooldownEnvVar  = "REBOOT_COOLDOWN_TIME"
	rebootCooldownDefault = 1 * time.Minute

	// the more VFs, the more likely the bug to trigger
	minimumAllocatableDevices = 32
)

var (
	workloadLabels = map[string]string{
		"app": "devaccess-test",
	}
)

var _ = Describe("[disruptive][node][kubelet][devicemanager] Device management tests", func() {
	defer GinkgoRecover()

	var (
		// reused as restart cooldown, because restart is seen as lighter form of reboot
		rebootCooldownTime = rebootCooldownDefault

		targetNode              string
		sriovDeviceResourceName = sriovDeviceResourceNameDefault
		workloadContainerImage  = workloadContainerImageDefault
	)

	BeforeEach(func() {
		targetNode = os.Getenv(targetNodeEnvVar)
		if targetNode == "" {
			Skip(fmt.Sprintf("Need an explicit target node name, got none (env var: %q)", targetNodeEnvVar))
		}
		testlog.Infof("target node name: %q", targetNode)

		if val, ok := os.LookupEnv(sriovDeviceResourceNameEnvVar); ok {
			sriovDeviceResourceName = val
		}
		testlog.Infof("(SRIOV) device resource name: %q", sriovDeviceResourceName)

		if val, ok := os.LookupEnv(workloadContainerImageEnvVar); ok {
			workloadContainerImage = val
		}
		testlog.Infof("workload container image: %q", workloadContainerImage)

		var err error
		if val, ok := os.LookupEnv(rebootCooldownEnvVar); ok {
			rebootCooldownTime, err = time.ParseDuration(val)
			Expect(err).ToNot(HaveOccurred(), "error parsing user-provided cooldown: %v", val)
		}
		testlog.Infof("reboot cooldown time: %v", rebootCooldownTime)

		_, err = testnodes.GetByName(targetNode)
		Expect(err).ToNot(HaveOccurred(), "error getting the target node %q", targetNode)
	})

	It("Verify that pods requesting devices are correctly recovered on node restart", func() {
		namespace := testutils.NamespaceTesting

		// refresh the targetNode object
		node, err := testnodes.GetByName(targetNode)
		Expect(err).ToNot(HaveOccurred(), "error getting the target node %q", targetNode)

		// phase1: complete the node praparation: make sure we have enough devices. Short timeout, we should be idle now.
		allocatableDevices := waitForNodeToReportResourcesOrFail("pre reboot", targetNode, sriovDeviceResourceName, 2*time.Minute, 2*time.Second)
		Expect(allocatableDevices).To(BeNumerically(">=", minimumAllocatableDevices), "device %q has too low amount available - testing scenario unreliable", sriovDeviceResourceName, allocatableDevices)

		// phase2: node is prepared, run the test workload and check it gets the device it expected
		wlDp := makeWorkloadDeployment(namespace, workloadContainerImage, sriovDeviceResourceName, 1)
		err = testclient.Client.Create(context.TODO(), wlDp)
		Expect(err).ToNot(HaveOccurred(), "error creating workload deployment")

		// short timeout: we are on idle cluster
		wlDp = waitForDeploymentCompleteOrFail("pre reboot", *wlDp, 5*time.Minute)
		testlog.Infof("deployment %s/%s running", wlDp.Namespace, wlDp.Name)

		// phase3: the reboot, which trigger the very scenario we are after

		// managed, clean restart (e.g. `reboot` command or `systemctl reboot`
		// - details don't matter as long as this is a managed clean restart).
		// Power loss scenarios, aka hard reboot, deferred to another test.
		// intentionally ignoring error. We need to tolerate connection error or disconnect
		// because the node is rebooting.
		runCommandOnNodeThroughMCD(context.TODO(), node, "reboot", rebootNodeCommandMCD)
		// this is (likely) a SNO. We need to tolerate connection errors,
		// because the apiserver is going down as well.
		// we intentionally use a generous timeout.
		// On Bare metal reboot can take a while.
		waitForNodeReadyOrFail("post reboot", targetNode, 20*time.Minute, 3*time.Second)

		// are we really sure? we can't predict if we will have state flapping,
		// we can't predict if pods go back to containercreating and ideally we
		// should have no flapping.
		// Tracking all the state will make the test complex *and fragile*.
		// The best we can do right now is to let the SNO cool down and check again.
		testlog.Infof("post reboot: entering cooldown time: %v", rebootCooldownTime)
		time.Sleep(rebootCooldownTime)
		testlog.Infof("post reboot: finished cooldown time: %v", rebootCooldownTime)

		// longer timeout. We expect things to be still on flux
		waitForNodeToReportResourcesOrFail("post reboot", targetNode, sriovDeviceResourceName, 10*time.Minute, 2*time.Second)

		// from now on we assume the node is somehow stable again and we stop swallowing the connection errors

		// what happened to the previous workload? Note we need a long timeout, because we have no means to learn when the SNO is actually settled
		wlDp = waitForDeploymentCompleteOrFail("post reboot", *wlDp, 30*time.Minute)
		testlog.Infof("deployment %s/%s running", wlDp.Namespace, wlDp.Name)

		// do we have any pods from the workload deployment on UnexpectedAdmissionError?
		// we usually do, but note it's legit to not have them at all if we got lucky.
		pods, err := listPodsByDeployment(testclient.Client, context.TODO(), *wlDp)
		Expect(err).ToNot(HaveOccurred(), "error checking the pods belonging to deployment %s/%s", wlDp.Namespace, wlDp.Name)
		admissionFailed := 0
		for idx := range pods {
			if isUnexpectedAdmissionError(pods[idx], sriovDeviceResourceName) {
				admissionFailed++
			}
		}
		testlog.Infof("workload deployment %s/%s got %d admission errors (created %d pods to go running)", wlDp.Namespace, wlDp.Name, admissionFailed, len(pods))

		// phase4: sanity check that a new workload works as expected
		wlPod := makeWorkloadPod(namespace, "workload-reboot-post", workloadContainerImage, sriovDeviceResourceName)

		err = testclient.Client.Create(context.TODO(), wlPod)
		Expect(err).ToNot(HaveOccurred(), "error creating workload pod post reboot")

		// things should be settled now so we can use again a short timeout
		testlog.Infof("post reboot: running a fresh pod %s/%s resource=%q", wlPod.Namespace, wlPod.Name, sriovDeviceResourceName)
		updatedPod, err := testpods.WaitForPredicate(context.TODO(), client.ObjectKeyFromObject(wlPod), 1*time.Minute, func(pod *corev1.Pod) (bool, error) {
			return isPodReady(*pod), nil
		})
		Expect(err).ToNot(HaveOccurred(), "error checking the workload pod post reboot")
		testlog.Infof("post reboot: newer workload pod %s/%s admitted: %s", updatedPod.Namespace, updatedPod.Name, extractPodStatus(updatedPod.Status))
	})

	It("Verify that pods requesting devices are not disrupted by a kubelet restart", func() {
		namespace := testutils.NamespaceTesting

		// refresh the targetNode object
		node, err := testnodes.GetByName(targetNode)
		Expect(err).ToNot(HaveOccurred(), "error getting the target node %q", targetNode)

		// phase1: complete the node praparation: make sure we have enough devices. Short timeout, we should be idle now.
		allocatableDevices := waitForNodeToReportResourcesOrFail("pre reboot", targetNode, sriovDeviceResourceName, 2*time.Minute, 2*time.Second)
		// 2 pods, 1 container each, 1 device requested per container
		Expect(allocatableDevices).To(BeNumerically(">=", 2), "device %q has too low amount available (%d)", sriovDeviceResourceName, allocatableDevices)

		// phase2: node is prepared, run the test workload and check it gets the device it expected
		wlPod := makeWorkloadPod(namespace, "workload-restart-pre", workloadContainerImage, sriovDeviceResourceName)
		err = testclient.Client.Create(context.TODO(), wlPod)
		Expect(err).ToNot(HaveOccurred(), "error creating workload pod")

		// short timeout: we are on idle cluster
		updatedPod, err := testpods.WaitForPredicate(context.TODO(), client.ObjectKeyFromObject(wlPod), 3*time.Minute, func(pod *corev1.Pod) (bool, error) {
			return isPodReady(*pod), nil
		})
		Expect(err).ToNot(HaveOccurred(), "error waiting for the workload pod to be ready - pre restart")
		podUID := updatedPod.UID // shortcut to the reference
		testlog.Infof("pod %q %s/%s ready", podUID, updatedPod.Namespace, updatedPod.Name)

		// phase3: the kubelet restart
		runCommandOnNodeThroughMCD(context.TODO(), node, "kubelet restart", kubeletRestartCommandMCD)

		waitForNodeReadyOrFail("post restart", targetNode, 20*time.Minute, 3*time.Second)

		// are we really sure? we can't predict if we will have state flapping,
		// we can't predict if pods go back to containercreating and ideally we
		// should have no flapping.
		// Tracking all the state will make the test complex *and fragile*.
		// The best we can do right now is to let the SNO cool down and check again.
		testlog.Infof("post restart: entering cooldown time: %v", rebootCooldownTime)
		time.Sleep(rebootCooldownTime)
		testlog.Infof("post restart: finished cooldown time: %v", rebootCooldownTime)

		// longer timeout. We expect things to be still on flux
		postRestartPod, err := testpods.WaitForPredicate(context.TODO(), client.ObjectKeyFromObject(wlPod), 10*time.Minute, func(pod *corev1.Pod) (bool, error) {
			return isPodReady(*pod), nil
		})
		Expect(err).ToNot(HaveOccurred(), "error waiting for the workload pod to be ready - post restart")
		testlog.Infof("pod %q %s/%s ready", postRestartPod.UID, postRestartPod.Namespace, postRestartPod.Name)

		Expect(postRestartPod.UID).To(Equal(podUID), "pod recreated post kubelet restart: UID %q -> %q", podUID, postRestartPod.UID)

		// phase4: sanity check that a new workload works as expected
		wlPod2 := makeWorkloadPod(namespace, "workload-restart-post", workloadContainerImage, sriovDeviceResourceName)

		err = testclient.Client.Create(context.TODO(), wlPod2)
		Expect(err).ToNot(HaveOccurred(), "error creating workload pod post kubelet restart")

		// things should be settled now so we can use again a short timeout
		testlog.Infof("post restart: running a fresh pod %s/%s resource=%q", wlPod2.Namespace, wlPod2.Name, sriovDeviceResourceName)
		updatedPod2, err := testpods.WaitForPredicate(context.TODO(), client.ObjectKeyFromObject(wlPod2), 1*time.Minute, func(pod *corev1.Pod) (bool, error) {
			return isPodReady(*pod), nil
		})
		Expect(err).ToNot(HaveOccurred(), "error checking the workload pod post kubelet restart")
		testlog.Infof("post restart: newer workload pod %s/%s admitted: %s", updatedPod2.Namespace, updatedPod2.Name, extractPodStatus(updatedPod2.Status))
	})
})

func makeWorkloadPodSpec(imageName, resourceName string) corev1.PodSpec {
	true_ := true
	false_ := false
	zero := int64(0)
	rl := corev1.ResourceList{
		corev1.ResourceCPU:                *resource.NewQuantity(2, resource.DecimalSI),
		corev1.ResourceMemory:             resource.MustParse("512Mi"),
		corev1.ResourceName(resourceName): *resource.NewQuantity(1, resource.DecimalSI),
	}
	return corev1.PodSpec{
		RestartPolicy:                 corev1.RestartPolicyAlways,
		TerminationGracePeriodSeconds: &zero,
		Containers: []corev1.Container{{
			Image:           imageName,
			ImagePullPolicy: corev1.PullAlways,
			Name:            "devaccess-container",
			Resources: corev1.ResourceRequirements{
				Limits:   rl,
				Requests: rl,
			},
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
				AllowPrivilegeEscalation: &false_,
				Privileged:               &false_,
				ReadOnlyRootFilesystem:   &true_,
				RunAsNonRoot:             &true_,
			},
		}},
	}
}

func makeWorkloadPod(namespace, name, imageName, resourceName string) *corev1.Pod {
	podDef := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    workloadLabels,
		},
		Spec: makeWorkloadPodSpec(imageName, resourceName),
	}
	return &podDef
}

func makeWorkloadDeployment(namespace, imageName, resourceName string, replicas int) *appsv1.Deployment {
	replicaCount := int32(replicas)
	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "devaccess-workload-deployment",
			Namespace: namespace,
			Labels:    workloadLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicaCount,
			Selector: &metav1.LabelSelector{
				MatchLabels: workloadLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					// note we do NOT use the annotation with the network.
					// this flow is still broken.
					// Workload pods have to require devices.
					Labels: workloadLabels,
				},
				Spec: makeWorkloadPodSpec(imageName, resourceName),
			},
		},
	}
	return &deployment
}

func waitForDeploymentCompleteOrFail(tag string, dp appsv1.Deployment, timeout time.Duration) *appsv1.Deployment {
	testlog.Infof("%s: wait for deployment %s/%s to be ready", tag, dp.Namespace, dp.Name)
	newDp := &appsv1.Deployment{}
	key := client.ObjectKeyFromObject(&dp)
	EventuallyWithOffset(1, func() (bool, error) {
		err := testclient.Client.Get(context.TODO(), key, newDp)
		if err != nil {
			return false, err
		}
		ready := isDeploymentComplete(&dp, &newDp.Status)
		testlog.Infof("deployment %s/%s replicas=%d updated=%d available=%d", dp.Namespace, dp.Name, newDp.Status.Replicas, newDp.Status.UpdatedReplicas, newDp.Status.AvailableReplicas)
		return ready, nil
	}).WithTimeout(timeout).WithPolling(5*time.Second).Should(BeTrue(), "cannot get the ready status of deployment %s/%s", dp.Namespace, dp.Name)
	testlog.Infof("%s: reported deployment %s/%s ready", tag, dp.Namespace, dp.Name)
	return newDp
}

func isDeploymentComplete(dp *appsv1.Deployment, newStatus *appsv1.DeploymentStatus) bool {
	return areDeploymentReplicasAvailable(newStatus, *(dp.Spec.Replicas)) &&
		newStatus.ObservedGeneration >= dp.Generation
}

func areDeploymentReplicasAvailable(newStatus *appsv1.DeploymentStatus, replicas int32) bool {
	return newStatus.UpdatedReplicas == replicas &&
		newStatus.Replicas == replicas &&
		newStatus.AvailableReplicas == replicas
}

func waitForNodeToReportResourcesOrFail(tag, nodeName, resourceName string, timeout, polling time.Duration) int64 {
	testlog.Infof("%s: wait for target node %q to report resource %q", tag, nodeName, resourceName)
	var allocatableDevs int64
	EventuallyWithOffset(1, func() (bool, error) {
		node, err := testnodes.GetByName(nodeName)
		if err != nil {
			// intentionally tolerate error
			testlog.Infof("wait for node %q to report resources: %v", nodeName, err)
			return false, nil
		}
		allocatableDevs = countDeviceAllocatable(node, resourceName)
		testlog.Infof("node %q resource=%s allocatable=%v", nodeName, resourceName, allocatableDevs)
		return allocatableDevs > 0, nil
	}).WithTimeout(timeout).WithPolling(polling).Should(BeTrue(), "cannot get the allocatable resource %q status on %q", nodeName)
	testlog.Infof("%s: reporting resources from node %q: %v=%d", tag, nodeName, resourceName, allocatableDevs)
	return allocatableDevs
}

func waitForNodeReadyOrFail(tag, nodeName string, timeout, polling time.Duration) {
	testlog.Infof("%s: waiting for node %q: to be ready", tag, nodeName)
	EventuallyWithOffset(1, func() (bool, error) {
		node, err := testnodes.GetByName(nodeName)
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

func runCommandOnNodeThroughMCD(ctx context.Context, node *corev1.Node, description, command string) (string, error) {
	testlog.Infof("node %q: before %s", node.Name, description)
	out, err := testnodes.ExecCommand(ctx, node, []string{"sh", "-c", command})
	testlog.Infof("node %q: output=[%s]", node.Name, string(out))
	testlog.Infof("node %q: after %s", node.Name, description)
	return string(out), err
}

func countDeviceAllocatable(node *corev1.Node, resourceName string) int64 {
	val, ok := node.Status.Allocatable[corev1.ResourceName(resourceName)]
	if !ok {
		return 0
	}
	return val.Value()
}

func isNodeReady(node corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isUnexpectedAdmissionError(pod corev1.Pod, resourceName string) bool {
	if pod.Status.Phase != corev1.PodFailed {
		return false
	}
	if pod.Status.Reason != "UnexpectedAdmissionError" {
		return false
	}
	if !strings.Contains(pod.Status.Message, "Allocate failed") {
		return false
	}
	if !strings.Contains(pod.Status.Message, "unhealthy devices "+resourceName) {
		return false
	}
	return true
}

func isPodReady(pod corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type != corev1.PodReady {
			continue
		}
		return cond.Status == corev1.ConditionTrue
	}
	return false
}

func extractPodStatus(podStatus corev1.PodStatus) string {
	return fmt.Sprintf("phase=%q reason=%q message=%q", podStatus.Phase, podStatus.Reason, podStatus.Message)
}

func listPodsByDeployment(cli client.Client, ctx context.Context, deployment appsv1.Deployment) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	sel, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}

	err = cli.List(ctx, podList, &client.ListOptions{Namespace: deployment.Namespace, LabelSelector: sel})
	if err != nil {
		return nil, err
	}

	return podList.Items, nil
}
