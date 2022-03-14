package pods

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/images"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/namespaces"
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
func WaitForDeletion(pod *corev1.Pod, timeout time.Duration) error {
	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		pod := &corev1.Pod{}
		if err := testclient.Client.Get(context.TODO(), key, pod); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// WaitForCondition waits until the pod will have specified condition type with the expected status
func WaitForCondition(pod *corev1.Pod, conditionType corev1.PodConditionType, conditionStatus corev1.ConditionStatus, timeout time.Duration) error {
	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatedPod := &corev1.Pod{}
		if err := testclient.Client.Get(context.TODO(), key, updatedPod); err != nil {
			return false, nil
		}

		for _, c := range updatedPod.Status.Conditions {
			if c.Type == conditionType && c.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForPredicate waits until the given predicate against the pod returns true or error.
func WaitForPredicate(pod *corev1.Pod, timeout time.Duration, pred func(pod *corev1.Pod) (bool, error)) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatedPod := &corev1.Pod{}
		if err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(pod), updatedPod); err != nil {
			return false, nil
		}

		ret, err := pred(updatedPod)
		if err != nil {
			return false, err
		}
		return ret, nil
	})
}

// WaitForPhase waits until the pod will have specified phase
func WaitForPhase(pod *corev1.Pod, phase corev1.PodPhase, timeout time.Duration) error {
	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatedPod := &corev1.Pod{}
		if err := testclient.Client.Get(context.TODO(), key, updatedPod); err != nil {
			return false, nil
		}

		if updatedPod.Status.Phase == phase {
			return true, nil
		}

		return false, nil
	})
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
func ExecCommandOnPod(c *kubernetes.Clientset, pod *corev1.Pod, command []string) ([]byte, error) {
	var outputBuf bytes.Buffer
	var errorBuf bytes.Buffer

	req := c.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: pod.Spec.Containers[0].Name,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: &outputBuf,
		Stderr: &errorBuf,
		Tty:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to run command %v: output %s; error %s", command, outputBuf.String(), errorBuf.String())
	}

	if errorBuf.Len() != 0 {
		return nil, fmt.Errorf("failed to run command %v: output %s; error %s", command, outputBuf.String(), errorBuf.String())
	}

	return outputBuf.Bytes(), nil
}

func WaitForPodOutput(c *kubernetes.Clientset, pod *corev1.Pod, command []string) ([]byte, error) {
	var out []byte
	if err := wait.PollImmediate(15*time.Second, time.Minute, func() (done bool, err error) {
		out, err = ExecCommandOnPod(c, pod, command)
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
	if err := testclient.Client.Get(context.TODO(), key, updatedPod); err != nil {
		return "", err
	}
	for _, containerStatus := range updatedPod.Status.ContainerStatuses {
		if containerStatus.Name == containerName {
			return strings.Trim(containerStatus.ContainerID, "cri-o://"), nil
		}
	}
	return "", fmt.Errorf("failed to find the container ID for the container %q under the pod %q", containerName, pod.Name)
}

// GetPerformanceOperatorPod returns the pod running the Performance Profile Operator
func GetPerformanceOperatorPod() (*corev1.Pod, error) {
	selector, err := labels.Parse(fmt.Sprintf("%s=%s", "name", "performance-operator"))
	if err != nil {
		return nil, err
	}

	pods := &corev1.PodList{}

	opts := &client.ListOptions{LabelSelector: selector, Namespace: namespaces.PerformanceOperator}
	if err := testclient.Client.List(context.TODO(), pods, opts); err != nil {
		return nil, err
	}
	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("incorrect performance operator pods count: %d", len(pods.Items))
	}

	return &pods.Items[0], nil
}
