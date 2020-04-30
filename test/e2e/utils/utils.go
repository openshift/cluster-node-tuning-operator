package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

// GetNodesByRole returns a list of nodes that match a given role.
func GetNodesByRole(cs *framework.ClientSet, role string) ([]corev1.Node, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{fmt.Sprintf("node-role.kubernetes.io/%s", role): ""}).String(),
	}
	nodeList, err := cs.Nodes().List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("Couldn't get a list of nodes by role (%s): %v", role, err)
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
		return nil, fmt.Errorf("Couldn't get a list of tuned pods: %v", err)
	}

	if len(podList.Items) != 1 {
		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("Failed to find a tuned pod for node %s", node.Name)
		}
		return nil, fmt.Errorf("Too many (%d) tuned pods for node %s", len(podList.Items), node.Name)
	}
	return &podList.Items[0], nil
}

// GetSysctl returns a sysctl value for sysctlVar from inside a (tuned) pod.
func GetSysctl(sysctlVar string, pod *corev1.Pod) (val string, err error) {
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		var out []byte
		out, err = exec.Command("oc", "rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name,
			"sysctl", "-n", sysctlVar).CombinedOutput()
		if err != nil {
			// Failed to query a sysctl "sysctlVar" on pod.Name
			return false, nil
		}
		val = strings.TrimSpace(string(out))
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("Failed to retrieve sysctl value %s in pod %s: %v", sysctlVar, pod.Name, err)
	}

	return val, nil
}

// ExecCmdInPod executes a command with arguments in a pod.
func ExecCmdInPod(pod *corev1.Pod, args ...string) (string, error) {
	entryPoint := "oc"
	cmd := []string{"rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name}
	cmd = append(cmd, args...)

	b, err := exec.Command(entryPoint, cmd...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// GetFileInPod returns content for file from inside a (tuned) pod.
func GetFileInPod(pod *corev1.Pod, file string) (val string, err error) {
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		out, err := ExecCmdInPod(pod, "cat", file)
		if err != nil {
			// Failed to query a sysctl "sysctlVar" on pod.Name
			return false, nil
		}
		val = strings.TrimSpace(out)
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("Failed to retrieve %s content in pod %s: %v", file, pod.Name, err)
	}

	return val, nil
}

// EnsureSysctl makes sure a sysctl value for sysctlVar from inside a (tuned) pod
// is equal to valExp.  Returns an error otherwise.
func EnsureSysctl(pod *corev1.Pod, sysctlVar string, valExp string) (err error) {
	var val string
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		val, err = GetSysctl(sysctlVar, pod)
		if err != nil {
			return false, nil
		}

		if val != valExp {
			return false, nil
		}
		return true, nil
	})

	if val != valExp {
		return fmt.Errorf("sysctl %s=%s on %s, expected %s.", sysctlVar, val, pod.Name, valExp)
	}

	return nil
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
