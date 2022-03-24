package util

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

const (
	// The default master profile.  See: assets/tuned/manifests/default-cr-tuned.yaml
	DefaultMasterProfile = "openshift-control-plane"
	// The default worker profile.  See: assets/tuned/manifests/default-cr-tuned.yaml
	DefaultWorkerProfile = "openshift-node"
)

// Logf formats using the default formats for its operands and writes to
// ginkgo.GinkgoWriter and a newline is appended.  It returns the number of
// bytes written and any write error encountered.
func Logf(format string, args ...interface{}) (n int, err error) {
	return fmt.Fprintf(ginkgo.GinkgoWriter, format+"\n", args...)
}

// GetNodesByRole returns a list of nodes that match a given role.
func GetNodesByRole(cs *framework.ClientSet, role string) ([]corev1.Node, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{fmt.Sprintf("node-role.kubernetes.io/%s", role): ""}).String(),
	}
	nodeList, err := cs.Nodes().List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get a list of nodes by role (%s): %v", role, err)
	}
	return nodeList.Items, nil
}

// GetTunedForNode returns a Pod that runs on a given node.
func GetTunedForNode(cs *framework.ClientSet, node *corev1.Node) (*corev1.Pod, error) {
	listOptions := metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
		LabelSelector: labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}).String(),
	}

	podList, err := cs.Pods(ntoconfig.OperatorNamespace()).List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get a list of TuneD Pods: %v", err)
	}

	if len(podList.Items) != 1 {
		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("failed to find a TuneD Pod for node %s", node.Name)
		}
		return nil, fmt.Errorf("too many (%d) TuneD Pods for node %s", len(podList.Items), node.Name)
	}
	return &podList.Items[0], nil
}

// GetNodeTuningOperator returns the node tuning operator Pod.
// If more than one operator Pod is running will return the first Pod found.
func GetNodeTuningOperatorPod(cs *framework.ClientSet) (*corev1.Pod, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"name": "cluster-node-tuning-operator"}).String(),
	}

	podList, err := cs.Pods(ntoconfig.OperatorNamespace()).List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("couldn't list potential NTO operator Pods: %v", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("failed to find the cluster-node-tuning-operator Pods")
	}

	// Return the first operator pod if multiple are running
	return &podList.Items[0], nil
}

// execCommand executes command 'name' with arguments 'args' and optionally
// ('log') logs the output.  Returns captured standard output, standard error
// and the error returned.
func execCommand(log bool, name string, args ...string) (bytes.Buffer, bytes.Buffer, error) {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if log {
		Logf("run command '%s %v':\n  out=%s\n  err=%s\n  ret=%v",
			name, args, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err)
	}

	return stdout, stderr, err
}

// ExecAndLogCommand executes command 'name' with arguments 'args' and logs
// the output.  Returns captured standard output, standard error and the error
// returned.
func ExecAndLogCommand(name string, args ...string) (bytes.Buffer, bytes.Buffer, error) {
	return execCommand(true, name, args...)
}

// ExecCmdInPod executes command with arguments 'cmd' in Pod 'pod'.
func ExecCmdInPod(pod *corev1.Pod, cmd ...string) (string, error) {
	ocArgs := []string{"rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name}
	ocArgs = append(ocArgs, cmd...)

	stdout, stderr, err := execCommand(false, "oc", ocArgs...)
	if err != nil {
		return "", fmt.Errorf("failed to run %s in Pod %s:\n  out=%s\n  err=%s\n  ret=%v", cmd, pod.Name, stdout.String(), stderr.String(), err.Error())
	}

	return stdout.String(), nil
}

// waitForCmdOutputInPod runs command with arguments 'cmd' in Pod 'pod' at
// an interval 'interval' and retries for at most the duration 'duration'.
// If 'valExp' is not nil, it also expects standard output of the command with
// leading and trailing whitespace optionally ('trim') trimmed to match 'valExp'.
// The function returns the retrieved standard output and an error returned when
// running 'cmd'.  Non-nil error is also returned when standard output of 'cmd'
// did not match non-nil 'valExp' by the time duration 'duration' elapsed.
func waitForCmdOutputInPod(interval, duration time.Duration, pod *corev1.Pod, valExp *string, trim bool, cmd ...string) (string, error) {
	var (
		val, sTrimmed string
		err, explain  error
	)
	startTime := time.Now()
	err = wait.PollImmediate(interval, duration, func() (bool, error) {
		val, err = ExecCmdInPod(pod, cmd...)

		if err != nil {
			explain = fmt.Errorf("out=%s; err=%s", val, err.Error())
			return false, nil
		}
		if trim {
			val = strings.TrimSpace(val)
		}
		if valExp != nil && val != *valExp {
			return false, nil
		}
		return true, nil
	})
	sTrimmed = " "
	if trim {
		sTrimmed = "(leading/trailing whitespace trimmed) "
	}
	if valExp != nil && val != *valExp {
		return val, fmt.Errorf("command %s outputs %s %sin Pod %s, expected %s (waited %s): %v",
			cmd, val, sTrimmed, pod.Name, *valExp, time.Since(startTime), explain)
	}
	if err != nil {
		return val, fmt.Errorf("command %s outputs %s %sin Pod %s (waited %s): %v",
			cmd, val, sTrimmed, pod.Name, time.Since(startTime), explain)
	}

	return val, nil
}

// WaitForCmdInPod runs command with arguments 'cmd' in Pod 'pod' at an interval
// 'interval' and retries for at most the duration 'duration'.  The function
// returns the retrieved value of standard output of the command at its first
// successful ('error' == nil) execution and an error set in case the command
// did not run successfully by the time duration 'duration' elapsed.
func WaitForCmdInPod(interval, duration time.Duration, pod *corev1.Pod, cmd ...string) (string, error) {
	return waitForCmdOutputInPod(interval, duration, pod, nil, false, cmd...)
}

// WaitForCmdOutputInPod runs command with arguments 'cmd' in Pod 'pod' at an
// interval 'interval' and retries for at most the duration 'duration' expecting
// standard output of the command with leading and trailing whitespace optionally
// ('trim') trimmed to match 'valExp'.  The function returns the retrieved
// value and an error in case the values did not match by the time duration
// 'duration' elapsed.
func WaitForCmdOutputInPod(interval, duration time.Duration, pod *corev1.Pod, valExp string, trim bool, cmd ...string) (string, error) {
	return waitForCmdOutputInPod(interval, duration, pod, &valExp, trim, cmd...)
}

// WaitForSysctlInPod waits for a successful ('error' == nil) output of the
// "sysctl -n 'sysctlVar'" command inside Pod 'pod'.  The execution interval is
// 'interval' and retries last for at most the duration 'duration'.  Returns the
// sysctl value retrieved with leading and trailing whitespace removed and an
// error during the command execution.
func WaitForSysctlInPod(interval, duration time.Duration, pod *corev1.Pod, sysctlVar string) (string, error) {
	cmd := []string{"sysctl", "-n", sysctlVar}

	val, err := WaitForCmdInPod(interval, duration, pod, cmd...)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve sysctl value %s in Pod %s: %v", sysctlVar, pod.Name, err)
	}

	return strings.TrimSpace(val), err
}

// WaitForSysctlValueInPod blocks until the sysctl value for 'sysctlVar' from
// inside Pod 'pod' is equal to 'valExp'.  The execution interval to check the
// value is 'interval' and retries last for at most the duration 'duration'.
// Returns the sysctl value retrieved and an error during the last command
// execution.
func WaitForSysctlValueInPod(interval, duration time.Duration, pod *corev1.Pod, sysctlVar string, valExp string) (string, error) {
	cmd := []string{"sysctl", "-n", sysctlVar}

	val, err := WaitForCmdOutputInPod(interval, duration, pod, valExp, true, cmd...)
	if val != valExp {
		return "", fmt.Errorf("sysctl %s=%s in Pod %s, expected %s: %v", sysctlVar, val, pod.Name, valExp, err)
	}

	return val, err
}

// WaitForClusterOperatorConditionStatus blocks until the NTO ClusterOperator
// condition 'conditionType' Status is equal to the value of 'conditionStatus'.
// The execution interval to check the value is 'interval' and retries last
// for at most the duration 'duration'.
func WaitForClusterOperatorConditionStatus(cs *framework.ClientSet, interval, duration time.Duration,
	conditionType configv1.ClusterStatusConditionType, conditionStatus configv1.ConditionStatus) error {
	var explain error

	startTime := time.Now()
	if err := wait.PollImmediate(interval, duration, func() (bool, error) {
		co, err := cs.ClusterOperators().Get(context.TODO(), tunedv1.TunedClusterOperatorResourceName, metav1.GetOptions{})
		if err != nil {
			explain = err
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == conditionType &&
				cond.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return errors.Wrapf(err, "failed to wait for ClusterOperator/%s condition %s status %s (waited %s): %v",
			tunedv1.TunedClusterOperatorResourceName, conditionType, conditionStatus, time.Since(startTime), explain)
	}
	return nil
}

// WaitForClusterOperatorConditionReason blocks until the NTO ClusterOperator
// condition 'conditionType' Reason is equal to the value of 'conditionReason'.
// The execution interval to check the value is 'interval' and retries last
// for at most the duration 'duration'.
func WaitForClusterOperatorConditionReason(cs *framework.ClientSet, interval, duration time.Duration,
	conditionType configv1.ClusterStatusConditionType, conditionReason string) error {
	var explain error

	startTime := time.Now()
	if err := wait.PollImmediate(interval, duration, func() (bool, error) {
		co, err := cs.ClusterOperators().Get(context.TODO(), tunedv1.TunedClusterOperatorResourceName, metav1.GetOptions{})
		if err != nil {
			explain = err
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == conditionType &&
				cond.Reason == conditionReason {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return errors.Wrapf(err, "failed to wait for ClusterOperator/%s condition %s reason %s (waited %s): %v",
			tunedv1.TunedClusterOperatorResourceName, conditionType, conditionReason, time.Since(startTime), explain)
	}
	return nil
}

// WaitForProfileConditionStatus blocks until Profile with name `profile`
// is reporting its `conditionType` with the value of 'conditionStatus',
// for the TuneD profile `profileExpect`.
// The execution interval to check the value is 'interval' and retries last
// for at most the duration 'duration'.
func WaitForProfileConditionStatus(cs *framework.ClientSet, interval, duration time.Duration, profile string, profileExpect string,
	conditionType tunedv1.ProfileConditionType, conditionStatus corev1.ConditionStatus) error {
	var explain error

	startTime := time.Now()
	if err := wait.PollImmediate(interval, duration, func() (bool, error) {
		p, err := cs.Profiles(ntoconfig.OperatorNamespace()).Get(context.TODO(), profile, metav1.GetOptions{})
		if err != nil {
			explain = err
			return false, nil
		}

		if p.Status.TunedProfile != profileExpect {
			explain = fmt.Errorf("Profile/%s reports TuneD profile %s, expected %s", profile, p.Status.TunedProfile, profileExpect)
			return false, nil
		}

		for _, cond := range p.Status.Conditions {
			if cond.Type == conditionType &&
				cond.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return errors.Wrapf(err, "failed to wait for Profile/%s condition %s status %s (waited %s): %v",
			profile, conditionType, conditionStatus, time.Since(startTime), explain)
	}
	return nil
}

// GetUpdatedMachineCountForPool returns the UpdatedMachineCount for MCP 'pool'.
func GetUpdatedMachineCountForPool(cs *framework.ClientSet, pool string) (int32, error) {
	mcp, err := cs.MachineConfigPools().Get(context.TODO(), pool, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	return mcp.Status.UpdatedMachineCount, nil
}

// WaitForPoolMachineCount polls a pool until its machineCount equals to 'count'.
func WaitForPoolMachineCount(cs *framework.ClientSet, pool string, count int32) error {
	startTime := time.Now()
	if err := wait.Poll(5*time.Second, 20*time.Minute, func() (bool, error) {
		mcp, err := cs.MachineConfigPools().Get(context.TODO(), pool, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if mcp.Status.MachineCount == count {
			return true, nil
		}
		return false, nil
	}); err != nil {
		return errors.Wrapf(err, "pool %s MachineCount != %d (waited %s)", pool, count, time.Since(startTime))
	}
	return nil
}

// WaitForPoolUpdatedMachineCount polls a pool until its UpdatedMachineCount equals to 'count'.
func WaitForPoolUpdatedMachineCount(cs *framework.ClientSet, pool string, count int32) error {
	startTime := time.Now()
	if err := wait.Poll(5*time.Second, 20*time.Minute, func() (bool, error) {
		mcp, err := cs.MachineConfigPools().Get(context.TODO(), pool, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if mcp.Status.UpdatedMachineCount == count {
			return true, nil
		}
		return false, nil
	}); err != nil {
		return errors.Wrapf(err, "pool %s UpdatedMachineCount != %d (waited %s)", pool, count, time.Since(startTime))
	}
	return nil
}

// GetDefaultWorkerProfile returns name of the default out-of-the-box TuneD profile for a node.
// See: assets/tuned/manifests/default-cr-tuned.yaml
func GetDefaultWorkerProfile(node *corev1.Node) string {
	_, master := node.Labels["node-role.kubernetes.io/master"]
	_, infra := node.Labels["node-role.kubernetes.io/infra"]

	if master || infra {
		return DefaultMasterProfile
	}

	return DefaultWorkerProfile
}
