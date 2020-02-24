// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/framework"
)

// Test the ClusterOperator node-tuning object exists and is Available.
func TestOperatorAvailable(t *testing.T) {
	cs := framework.NewClientSet()

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		co, err := cs.ClusterOperators().Get(context.TODO(), tunedv1.TunedClusterOperatorResourceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == configv1.OperatorAvailable &&
				cond.Status == configv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Did not get expected available condition: %v", err)
	}
}

// Test the default tuned CR exists.
func TestDefaultTunedExists(t *testing.T) {
	cs := framework.NewClientSet()

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		_, err := cs.Tuneds(ntoconfig.OperatorNamespace()).Get(context.TODO(), tunedv1.TunedDefaultResourceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Failed to get default tuned: %v", err)
	}
}

// Test the basic functionality of NTO and its operands.  The default sysctl(s)
// need(s) to be set across the nodes.
func TestWorkerNodeSysctl(t *testing.T) {
	sysctlVar := "net.ipv4.neigh.default.gc_thresh1"
	cs := framework.NewClientSet()

	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Ensuring the default worker node profile was set")
	err = ensureSysctl(pod, sysctlVar, "8192")
	if err != nil {
		t.Fatal(err)
	}
}

// Test the application (and rollback) of a custom profile via pod labelling.
func TestCustomProfileElasticSearch(t *testing.T) {
	const (
		profileElasticSearch  = "../../examples/elasticsearch.yaml"
		podLabelElasticSearch = "tuned.openshift.io/elasticsearch"
		sysctlVar             = "vm.max_map_count"
	)

	cs := framework.NewClientSet()

	t.Logf("Getting a list of worker nodes")
	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current value of %s in pod %s", sysctlVar, pod.Name)
	valOrig, err := getSysctl(sysctlVar, pod)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Labelling pod %s with label %s", pod.Name, podLabelElasticSearch)
	out, err := exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"=").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Creating the custom elasticsearch profile %s", profileElasticSearch)
	out, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the custom worker node profile was set")
	err = ensureSysctl(pod, sysctlVar, "262144")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Removing label %s from pod %s", podLabelElasticSearch, pod.Name)
	out, err = exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"-").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name)
	err = ensureSysctl(pod, sysctlVar, valOrig)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Deleting the custom elasticsearch profile %s", profileElasticSearch)
	out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}
}

// Test the application (and rollback) of a custom profile via node labelling.
func TestCustomProfileHugepages(t *testing.T) {
	const (
		profileHugepages   = "../../examples/hugepages.yaml"
		nodeLabelHugepages = "tuned.openshift.io/hugepages"
		sysctlVar          = "vm.nr_hugepages"
	)

	cs := framework.NewClientSet()

	t.Logf("Getting a list of worker nodes")
	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current value of %s in pod %s", sysctlVar, pod.Name)
	valOrig, err := getSysctl(sysctlVar, pod)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Labelling node %s with label %s", node.Name, nodeLabelHugepages)
	out, err := exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelHugepages+"=").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Creating the custom hugepages profile %s", profileHugepages)
	out, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the custom worker node profile was set")
	err = ensureSysctl(pod, sysctlVar, "16")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Deleting the custom hugepages profile %s", profileHugepages)
	out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name)
	err = ensureSysctl(pod, sysctlVar, valOrig)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Removing label %s from node %s", nodeLabelHugepages, node.Name)
	out, err = exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelHugepages+"-").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}
}

// Test the MachineConfig labels matching functionality and rollback.
func TestMachineConfigLabelsMatching(t *testing.T) {
	const (
		profileRealtime   = "../../examples/realtime-mc.yaml"
		mcpRealtime       = "../../examples/realtime-mcp.yaml"
		nodeLabelRealtime = "node-role.kubernetes.io/worker-rt"
		procCmdline       = "/proc/cmdline"
	)

	cs := framework.NewClientSet()

	t.Logf("Getting a list of worker nodes")
	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	workerMachinesOrig, err := getUpdatedMachineCountForPool(t, cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current %s value in pod %s", procCmdline, pod.Name)
	cmdlineOrig, err := getFileInPod(pod, procCmdline)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineOrig)

	t.Logf("Labelling node %s with label %s", node.Name, nodeLabelRealtime)
	out, err := exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"=").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Creating custom realtime profile %s", profileRealtime)
	out, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Creating custom MachineConfigPool %s", mcpRealtime)
	out, err = exec.Command("oc", "create", "-f", mcpRealtime).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Waiting for worker-rt MachineConfigPool UpdatedMachineCount == 1")
	if err := waitForPoolUpdatedMachineCount(t, cs, "worker-rt", 1); err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current %s value in pod %s", procCmdline, pod.Name)
	cmdlineNew, err := getFileInPod(pod, procCmdline)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

	// Check the key kernel parameters for the realtime profile to be present in /proc/cmdline
	t.Logf("Ensuring the custom worker node profile was set")
	for _, v := range []string{"skew_tick", "intel_pstate=disable", "nosoftlockup", "tsc=nowatchdog"} {
		if !strings.Contains(cmdlineNew, v) {
			t.Fatalf("Missing '%s' in %s: %s", v, procCmdline, cmdlineNew)
		}
	}

	// Node label needs to be removed first, and we also need to wait for the worker pool to complete the update;
	// otherwise worker-rt MachineConfigPool deletion would cause Degraded state of the worker pool.
	// see rhbz#1816239
	t.Logf("Removing label %s from node %s", nodeLabelRealtime, node.Name)
	out, err = exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Deleting the custom realtime profile %s", profileRealtime)
	out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	// Wait for the worker machineCount to go to the original value when the node was not part of worker-rt pool.
	t.Logf("Waiting for worker UpdatedMachineCount == %d", workerMachinesOrig)
	if err := waitForPoolUpdatedMachineCount(t, cs, "worker", workerMachinesOrig); err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current %s value in pod %s", procCmdline, pod.Name)
	cmdlineNew, err = getFileInPod(pod, procCmdline)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

	t.Logf("Ensuring the original kernel command line was restored")
	if cmdlineOrig != cmdlineNew {
		t.Fatal(fmt.Errorf("Kernel parameters as retrieved from %s after profile rollback do not match", procCmdline))
	}

	t.Logf("Deleting custom MachineConfigPool %s", mcpRealtime)
	out, err = exec.Command("oc", "delete", "-f", mcpRealtime).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}
}

// Returns a list of nodes that match a given role.
func getNodesByRole(cs *framework.ClientSet, role string) ([]corev1.Node, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{fmt.Sprintf("node-role.kubernetes.io/%s", role): ""}).String(),
	}
	nodeList, err := cs.Nodes().List(context.TODO(), listOptions)
	if err != nil {
		return nil, fmt.Errorf("Couldn't get a list of nodes by role (%s): %v", role, err)
	}
	return nodeList.Items, nil
}

// Returns a pod that runs on a given node.
func getTunedForNode(cs *framework.ClientSet, node *corev1.Node) (*corev1.Pod, error) {
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

// Returns a sysctl value for sysctlVar from inside a (tuned) pod.
func getSysctl(sysctlVar string, pod *corev1.Pod) (val string, err error) {
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

func execCmdInPod(pod *corev1.Pod, args ...string) (string, error) {
	entryPoint := "oc"
	cmd := []string{"rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name}
	cmd = append(cmd, args...)

	b, err := exec.Command(entryPoint, cmd...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// getFileInPod returns content for file from inside a (tuned) pod.
func getFileInPod(pod *corev1.Pod, file string) (val string, err error) {
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		out, err := execCmdInPod(pod, "cat", file)
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

// Makes sure a sysctl value for sysctlVar from inside a (tuned) pod is equal to valExp.
// Returns an error otherwise.
func ensureSysctl(pod *corev1.Pod, sysctlVar string, valExp string) (err error) {
	var val string
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		val, err = getSysctl(sysctlVar, pod)
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

// getUpdatedMachineCountForPool returns the UpdatedMachineCount for pool 'pool'.
func getUpdatedMachineCountForPool(t *testing.T, cs *framework.ClientSet, pool string) (int32, error) {
	mcp, err := cs.MachineConfigPools().Get(context.TODO(), pool, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	return mcp.Status.UpdatedMachineCount, nil
}

// waitForPoolMachineCount polls a pool until its machineCount equals to 'count'.
func waitForPoolMachineCount(t *testing.T, cs *framework.ClientSet, pool string, count int32) error {
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
	t.Logf("Pool %s has MachineCount == %d (waited %v)", pool, count, time.Since(startTime))
	return nil
}

// waitForPoolUpdatedMachineCount polls a pool until its UpdatedMachineCount equals to 'count'.
func waitForPoolUpdatedMachineCount(t *testing.T, cs *framework.ClientSet, pool string, count int32) error {
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
	t.Logf("Pool %s has UpdatedMachineCount == %d (waited %v)", pool, count, time.Since(startTime))
	return nil
}
