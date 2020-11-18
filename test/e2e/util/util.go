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

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

func Logf(format string, args ...interface{}) {
	fmt.Fprintf(ginkgo.GinkgoWriter, format+"\n", args...)
}

func execCommand(log bool, name string, arg ...string) (bytes.Buffer, bytes.Buffer, error) {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	cmd := exec.Command(name, arg...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if log {
		Logf("run command '%s %v':\n  out=%s\n  err=%s\n  ret=%v",
			name, arg, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err)
	}

	return stdout, stderr, err
}

func ExecAndLogCommand(name string, arg ...string) (bytes.Buffer, bytes.Buffer, error) {
	return execCommand(true, name, arg...)
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

// GetTunedForNode returns a pod that runs on a given node.
func GetTunedForNode(cs *framework.ClientSet, node *corev1.Node) (*corev1.Pod, error) {
	listOptions := metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
	}
	listOptions.LabelSelector = labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}).String()

	podList, err := cs.Pods(ntoconfig.OperatorNamespace()).List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get a list of tuned pods: %v", err)
	}

	if len(podList.Items) != 1 {
		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("failed to find a tuned pod for node %s", node.Name)
		}
		return nil, fmt.Errorf("too many (%d) tuned pods for node %s", len(podList.Items), node.Name)
	}
	return &podList.Items[0], nil
}

// GetSysctl returns a sysctl value for sysctlVar from inside a (tuned) pod.
func GetSysctl(sysctlVar string, pod *corev1.Pod) (string, error) {
	var (
		val          string
		err, explain error
	)
	err = wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		var out string
		out, err = ExecCmdInPod(pod, "sysctl", "-n", sysctlVar)
		if err != nil {
			explain = fmt.Errorf("out=%s; err=%s", out, err.Error())
			return false, nil
		}
		val = strings.TrimSpace(out)
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to retrieve sysctl value %s in pod %s: %v", sysctlVar, pod.Name, explain)
	}

	return val, nil
}

// ExecCmdInPod executes a command with arguments in a pod.
func ExecCmdInPod(pod *corev1.Pod, args ...string) (string, error) {
	entryPoint := "oc"
	cmd := []string{"rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name}
	cmd = append(cmd, args...)

	stdout, stderr, err := execCommand(false, entryPoint, cmd...)

	if err != nil {
		return "", fmt.Errorf("failed to run %s in Pod %s:\n  out=%s\n  err=%s\n  ret=%v", args, pod.Name, stdout.String(), stderr.String(), err.Error())
	}

	return stdout.String(), nil
}

// PollExecCmdInPod executes a command with arguments in a Pod
// until the command succeeds or times out.
func PollExecCmdInPod(interval, duration time.Duration, pod *corev1.Pod, args ...string) (string, error) {
	var (
		val     string
		explain error
	)
	err := wait.PollImmediate(interval, duration, func() (bool, error) {
		out, err := ExecCmdInPod(pod, args...)
		if err != nil {
			explain = fmt.Errorf("out=%s; err=%s", out, err.Error())
			return false, nil
		}
		val = out
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to run %s in Pod %s: %v", args, pod.Name, explain)
	}

	return val, nil
}

// GetFileInPod returns content for file from inside a (tuned) pod.
func GetFileInPod(pod *corev1.Pod, file string) (string, error) {
	return PollExecCmdInPod(5*time.Second, 5*time.Minute, pod, "cat", file)
}

// EnsureSysctl makes sure a sysctl value for sysctlVar from inside a (tuned) pod
// is equal to valExp.  Returns an error otherwise.
func EnsureSysctl(pod *corev1.Pod, sysctlVar string, valExp string) error {
	var (
		val     string
		explain error
	)
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		var err error
		val, err = GetSysctl(sysctlVar, pod)

		if err != nil {
			explain = err
			return false, nil
		}

		if val != valExp {
			return false, nil
		}
		return true, nil
	})
	if val != valExp {
		return fmt.Errorf("sysctl %s=%s on %s, expected %s: %v", sysctlVar, val, pod.Name, valExp, explain)
	}

	return nil
}

// EnsureCmdOutputInPod runs command 'args' in Pod 'pod' at an interval 'interval' and
// retries for at most duration 'duration' expecting standard output of the command with
// leadingand trailing whitespace trimmed to match 'valExp'.  The function returns the
// retrieved value and an error in case the values did not match or the command
// timed out after 'duration'.
func EnsureCmdOutputInPod(interval, duration time.Duration, valExp string, pod *corev1.Pod, args ...string) (string, error) {
	var (
		val     string
		explain error
	)
	err := wait.PollImmediate(interval, duration, func() (bool, error) {
		out, err := ExecCmdInPod(pod, args...)
		if err != nil {
			explain = fmt.Errorf("out=%s; err=%s", out, err.Error())
			return false, nil
		}
		val = strings.TrimSpace(out)
		if val != valExp {
			return false, nil
		}
		return true, nil
	})
	if val != valExp {
		return val, fmt.Errorf("command %s outputs (leading/trailing whitespace trimmed) %s in Pod %s, expected %s: %v", args, val, pod.Name, valExp, explain)
	}

	return val, err
}

// GetUpdatedMachineCountForPool returns the UpdatedMachineCount for pool 'pool'.
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
