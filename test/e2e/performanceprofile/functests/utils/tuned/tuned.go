package tuned

import (
	"context"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
)

func GetPod(ctx context.Context, node *corev1.Node) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	opts := &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}),
	}

	if err := testclient.Client.List(ctx, podList, opts); err != nil {
		return nil, fmt.Errorf("couldn't get a list of TuneD Pods; %w", err)
	}

	if len(podList.Items) != 1 {
		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("failed to find a TuneD Pod for node %s", node.Name)
		}
		return nil, fmt.Errorf("too many (%d) TuneD Pods for node %s", len(podList.Items), node.Name)
	}
	return &podList.Items[0], nil
}

func WaitForStalldTo(run bool, interval, timeout time.Duration, node *corev1.Node) error {
	return wait.PollImmediate(interval, timeout, func() (bool, error) {
		cmd := []string{"/bin/bash", "-c", "pidof stalld || echo \"stalld not running\""}
		stalldPid, err := nodes.ExecCommandOnNode(cmd, node)
		if err != nil {
			klog.Errorf("failed to execute command %q on node: %q; %v", cmd, node.Name, err)
			return false, err
		}
		_, err = strconv.Atoi(stalldPid)
		if !run { // we don't want stalld to run
			if err == nil {
				return false, nil
			}
			return true, nil
		}
		// we want stalld to run
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func CheckParameters(node *corev1.Node, sysctlMap map[string]string, kernelParameters []string, stalld, rtkernel bool) {
	cmd := []string{"/bin/bash", "-c", "pidof stalld || echo \"stalld not running\""}
	By(fmt.Sprintf("Executing %q", cmd))
	stalldPid, err := nodes.ExecCommandOnNode(cmd, node)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to execute command %q on node: %q; %v", cmd, node.Name, err)

	_, err = strconv.Atoi(stalldPid)
	if stalld {
		ExpectWithOffset(1, err).ToNot(HaveOccurred(),
			"node=%q does not have a running stalld process", node.Name)
	} else {
		ExpectWithOffset(1, err).To(HaveOccurred(),
			"node=%q stalld_pid=%q stalld is running when it shouldn't", node.Name, stalldPid)
	}

	key := types.NamespacedName{
		Name:      components.GetComponentName(testutils.PerformanceProfileName, components.ProfileNamePerformance),
		Namespace: components.NamespaceNodeTuningOperator,
	}

	tuned := &tunedv1.Tuned{}
	ExpectWithOffset(1, testclient.Client.Get(context.TODO(), key, tuned)).ToNot(HaveOccurred(),
		"cannot find the cluster Node Tuning Operator object "+key.String())

	if stalld {
		ExpectWithOffset(1, *tuned.Spec.Profile[0].Data).To(ContainSubstring("stalld=start,enable"),
			"node=%q cannot find substring stalld=start,enable in tuned profile data", node.Name)
	} else {
		ExpectWithOffset(1, *tuned.Spec.Profile[0].Data).To(ContainSubstring("stalld=stop,disable"),
			"node=%q cannot find substring stalld=stop,disable in tuned profile data", node.Name)
	}

	for param, expected := range sysctlMap {
		cmd = []string{"sysctl", "-n", param}
		By(fmt.Sprintf("Executing %q", cmd))
		out, err := nodes.ExecCommandOnNode(cmd, node)
		ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to execute command %q on node: %q", cmd, node.Name)
		ExpectWithOffset(1, out).Should(Equal(expected), "parameter %s value is not %s", param, expected)
	}

	cmd = []string{"cat", "/proc/cmdline"}
	By(fmt.Sprintf("Executing %q", cmd))
	cmdline, err := nodes.ExecCommandOnNode(cmd, node)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to execute command %q on node: %q", cmd, node.Name)

	for _, param := range kernelParameters {
		ExpectWithOffset(1, cmdline).To(ContainSubstring(param))
	}

	if !rtkernel {
		cmd = []string{"uname", "-a"}
		By(fmt.Sprintf("Executing %q", cmd))
		kernel, err := nodes.ExecCommandOnNode(cmd, node)
		ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to execute command %q on node: %q", cmd, node.Name)
		ExpectWithOffset(1, kernel).To(ContainSubstring("Linux"), "kernel should be Linux")

		err = nodes.HasPreemptRTKernel(node)
		ExpectWithOffset(1, err).To(HaveOccurred(), "node should have non-RT kernel")
	}
}

func GetProfile(ctx context.Context, cli client.Client, ns, name string) (*tunedv1.Profile, error) {
	key := client.ObjectKey{
		Namespace: ns,
		Name:      name,
	}
	p := &tunedv1.Profile{}
	err := cli.Get(ctx, key, p)
	if err != nil {
		return nil, err
	}
	return p, nil
}
