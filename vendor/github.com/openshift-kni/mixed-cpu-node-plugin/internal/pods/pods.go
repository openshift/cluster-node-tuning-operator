/*
 * Copyright 2023 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package pods

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	PauseImage          = "quay.io/openshift-kni/pause:test-ci"
	PauseCommand        = "/pause"
	NonTerminalSelector = "status.phase!=Failed,status.phase!=Succeeded"
)

type Options func(*corev1.Pod)

func Make(name, namespace string, opts ...Options) *corev1.Pod {
	var zero int64
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &zero,
			Containers: []corev1.Container{
				{
					Name:    name + "-cnt",
					Image:   PauseImage,
					Command: []string{PauseCommand},
				},
			},
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithLabels(labels map[string]string) func(pod *corev1.Pod) {
	return func(pod *corev1.Pod) {
		for k, v := range pod.Labels {
			labels[k] = v
		}
		pod.Labels = labels
	}
}

func WithRequests(list corev1.ResourceList, ids ...int) func(pod *corev1.Pod) {
	if ids == nil {
		ids = append(ids, 0)
	}
	return func(pod *corev1.Pod) {
		for _, id := range ids {
			pod.Spec.Containers[id].Resources.Requests = list
		}
	}
}

func WithLimits(list corev1.ResourceList, ids ...int) func(pod *corev1.Pod) {
	if ids == nil {
		ids = append(ids, 0)
	}
	return func(pod *corev1.Pod) {
		for _, id := range ids {
			pod.Spec.Containers[id].Resources.Limits = list
		}
	}
}

// GetAllowedCPUs returns a CPUSet of cpus that the pod's containers
// are allowed to access
func GetAllowedCPUs(c *kubernetes.Clientset, pod *corev1.Pod) (*cpuset.CPUSet, error) {
	// TODO is this reliable enough or we should check cgroups directly?
	cmd := []string{"/bin/sh", "-c", "grep Cpus_allowed_list /proc/self/status | cut -f2"}
	out, err := Exec(c, pod, cmd)
	if err != nil {
		return nil, err
	}

	cpus, err := cpuset.Parse(strings.Trim(string(out), "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse cpuset when input is: %s; %w", out, err)
	}
	return &cpus, nil
}

func Exec(c *kubernetes.Clientset, pod *corev1.Pod, command []string) ([]byte, error) {
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

	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: &outputBuf,
		Stderr: &errorBuf,
		Tty:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to run command: %v output %q; error %q; %w", command, outputBuf.String(), errorBuf.String(), err)
	}

	if errorBuf.Len() != 0 {
		return nil, fmt.Errorf("failed to run command: %v output %q; error %q", command, outputBuf.String(), errorBuf.String())
	}

	return outputBuf.Bytes(), nil
}

// OwnedByDeployment return list of pods owned by workload resources
func OwnedByDeployment(ctx context.Context, c client.Client, dp *appsv1.Deployment, opts *client.ListOptions) ([]corev1.Pod, error) {
	podlist := &corev1.PodList{}
	labelMap, err := metav1.LabelSelectorAsMap(dp.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to convert LabelSelector=%v to map; %w", dp.Spec.Selector, err)
	}
	opts.LabelSelector = labels.SelectorFromSet(labelMap)
	err = c.List(ctx, podlist, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods; %w", err)
	}
	return podlist.Items, nil
}

func PrintStatus(pods []corev1.Pod) string {
	podsInfo := strings.Builder{}
	for _, pod := range pods {
		_, _ = podsInfo.WriteString(pod.Namespace + "/" + pod.Name + "\n")
		podsInfo.WriteString(pod.Status.String() + "\n")
	}
	return podsInfo.String()
}
