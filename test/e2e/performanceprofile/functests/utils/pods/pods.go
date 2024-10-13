package pods

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
)

// DefaultDeletionTimeout contains the default pod deletion timeout in seconds
const DefaultDeletionTimeout = 120

// GetTestPod returns pod with the busybox image
func GetTestPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-",
			Labels: map[string]string{
				"test": "",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   images.Test(),
					Command: []string{"sleep", "10h"},
				},
			},
		},
	}
}

// WaitForDeletion waits until the pod will be removed from the cluster
func WaitForDeletion(ctx context.Context, pod *corev1.Pod, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		if err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(pod), pod); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// WaitForCondition waits until the pod will have specified condition type with the expected status
func WaitForCondition(ctx context.Context, podKey client.ObjectKey, conditionType corev1.PodConditionType, conditionStatus corev1.ConditionStatus, timeout time.Duration) (*corev1.Pod, error) {
	updatedPod := &corev1.Pod{}
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		if err := testclient.DataPlaneClient.Get(ctx, podKey, updatedPod); err != nil {
			return false, nil
		}
		for _, c := range updatedPod.Status.Conditions {
			if c.Type == conditionType && c.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	})
	return updatedPod, err
}

// WaitForPredicate waits until the given predicate against the pod returns true or error.
func WaitForPredicate(ctx context.Context, podKey client.ObjectKey, timeout time.Duration, pred func(pod *corev1.Pod) (bool, error)) (*corev1.Pod, error) {
	updatedPod := &corev1.Pod{}
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		if err := testclient.DataPlaneClient.Get(ctx, podKey, updatedPod); err != nil {
			return false, nil
		}

		ret, err := pred(updatedPod)
		if err != nil {
			return false, err
		}
		return ret, nil
	})
	return updatedPod, err
}

// WaitForPhase waits until the pod will have specified phase
func WaitForPhase(ctx context.Context, podKey client.ObjectKey, phase corev1.PodPhase, timeout time.Duration) (*corev1.Pod, error) {
	updatedPod := &corev1.Pod{}
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		if err := testclient.DataPlaneClient.Get(ctx, podKey, updatedPod); err != nil {
			return false, nil
		}

		if updatedPod.Status.Phase == phase {
			return true, nil
		}
		return false, nil
	})
	return updatedPod, err
}

// GetLogs returns logs of the specified pod
func GetLogs(c *kubernetes.Clientset, pod *corev1.Pod) (string, error) {
	logStream, err := c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).Stream(context.TODO())
	if err != nil {
		return "", err
	}
	defer logStream.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, logStream); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// ExecCommandOnPod runs command in the pod and returns buffer output
func ExecCommandOnPod(c *kubernetes.Clientset, pod *corev1.Pod, containerName string, command []string) ([]byte, error) {
	var outputBuf bytes.Buffer
	var errorBuf bytes.Buffer
	// if no name provided, take the first container from the pod
	if containerName == "" {
		containerName = pod.Spec.Containers[0].Name
	}

	req := c.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &outputBuf,
		Stderr: &errorBuf,
		Tty:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to run command %v: output %q; error %q; %w", command, outputBuf.String(), errorBuf.String(), err)
	}

	if errorBuf.Len() != 0 {
		return nil, fmt.Errorf("failed to run command %v: output %q; error %q", command, outputBuf.String(), errorBuf.String())
	}

	return outputBuf.Bytes(), nil
}

func WaitForPodOutput(ctx context.Context, c *kubernetes.Clientset, pod *corev1.Pod, command []string) ([]byte, error) {
	var out []byte
	if err := wait.PollUntilContextTimeout(ctx, 15*time.Second, time.Minute, true, func(ctx context.Context) (done bool, err error) {
		out, err = ExecCommandOnPod(c, pod, "", command)
		if err != nil {
			return false, err
		}

		return len(out) != 0, nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}

// GetContainerIDByName returns container ID under the pod by the container name
func GetContainerIDByName(pod *corev1.Pod, containerName string) (string, error) {
	updatedPod := &corev1.Pod{}
	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	if err := testclient.DataPlaneClient.Get(context.TODO(), key, updatedPod); err != nil {
		return "", err
	}
	// Find the container by name
	for _, containerStatus := range updatedPod.Status.ContainerStatuses {
		if containerStatus.Name == containerName {
			containerId, found := strings.CutPrefix(containerStatus.ContainerID, "cri-o://")
			if !found {
				return "", fmt.Errorf("Invalid ContainerID format: %q container %q", containerStatus.ContainerID, containerName)
			}
			return containerId, nil
		}
	}
	// Container not found in the pod
	return "", fmt.Errorf("failed to find the container ID for the container %q under the pod %q", containerName, pod.Name)
}

func DumpResourceRequirements(pod *corev1.Pod) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "resource requirements for pod %s/%s:\n", pod.Namespace, pod.Name)
	allContainers := []corev1.Container{}
	allContainers = append(allContainers, pod.Spec.Containers...)
	allContainers = append(allContainers, pod.Spec.InitContainers...)
	for _, container := range allContainers {
		fmt.Fprintf(&sb, "+- container %q: %s\n", container.Name, resourceListToString(container.Resources.Limits))
	}
	fmt.Fprintf(&sb, "---\n")
	return sb.String()
}

func GetPodsOnNode(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	pods := corev1.PodList{}
	listOptions := &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}),
	}
	err := testclient.DataPlaneClient.List(ctx, &pods, listOptions)
	return pods.Items, err
}

func resourceListToString(res corev1.ResourceList) string {
	idx := 0
	resNames := make([]string, len(res))
	for resName := range res {
		resNames[idx] = string(resName)
		idx++
	}
	sort.Strings(resNames)

	items := []string{}
	for _, resName := range resNames {
		resQty := res[corev1.ResourceName(resName)]
		items = append(items, fmt.Sprintf("%s=%s", resName, resQty.String()))
	}
	return strings.Join(items, ", ")
}

func CheckPODSchedulingFailed(c client.Client, pod *corev1.Pod) (bool, error) {
	events, err := events.GetEventsForObject(c, pod.Namespace, pod.Name, string(pod.UID))
	if err != nil {
		return false, err
	}
	if len(events.Items) == 0 {
		return false, fmt.Errorf("no event received for %s/%s", pod.Namespace, pod.Name)
	}

	for _, item := range events.Items {
		if item.Reason == "FailedScheduling" {
			return true, nil
		}
	}
	return false, nil
}
