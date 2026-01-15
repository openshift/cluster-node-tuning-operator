package baseload

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TODO: Currently this package only calculates CPU-based load. To support other
// resources like memory, etc. adjustments need to be made to the FromPods function
// and additional getter methods should be added to the Load type.
type Load struct {
	NodeName  string
	Resources corev1.ResourceList
}

// ForNode calculates the total resource load on a node from all running pods
func ForNode(ctx context.Context, cli client.Client, nodeName string) (Load, error) {
	pods, err := getPodsOnNode(ctx, cli, nodeName)
	if err != nil {
		return Load{NodeName: nodeName}, err
	}
	return FromPods(nodeName, pods), nil
}

// getPodsOnNode returns all running pods on the specified node
func getPodsOnNode(ctx context.Context, cli client.Client, nodeName string) ([]corev1.Pod, error) {
	sel, err := fields.ParseSelector("spec.nodeName=" + nodeName)
	if err != nil {
		return nil, err
	}

	podList := &corev1.PodList{}
	err = cli.List(ctx, podList, &client.ListOptions{FieldSelector: sel})
	if err != nil {
		return nil, err
	}

	var runningPods []corev1.Pod
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods = append(runningPods, pod)
		}
	}
	return runningPods, nil
}

// FromPods calculates the total CPU consumption from a list of pods
func FromPods(nodeName string, pods []corev1.Pod) Load {
	load := Load{
		NodeName:  nodeName,
		Resources: corev1.ResourceList{},
	}

	for _, pod := range pods {
		for _, cnt := range pod.Spec.Containers {
			if cpu, ok := cnt.Resources.Requests[corev1.ResourceCPU]; ok {
				qty := load.Resources[corev1.ResourceCPU]
				qty.Add(cpu)
				load.Resources[corev1.ResourceCPU] = qty
			}
		}
	}

	return load
}

// CPURequestedCores returns the total CPU request rounded up to whole cores
// using ceiling division: ⌈millicores / 1000⌉ (any fractional core counts as one full core).
// Examples: 0m → 0, 500m → 1, 1000m → 1, 1001m → 2.
func (l Load) CPURequestedCores() int {
	millis := l.Resources.Cpu().MilliValue()
	return int((millis + 999) / 1000)
}

// AvailableCPUs calculates how many CPUs are available given total isolated CPUs
// and the current load from non-guaranteed pods
func (l Load) AvailableCPUs(totalIsolatedCPUs int) int {
	return max(0, totalIsolatedCPUs-l.CPURequestedCores())
}

func (l Load) HasCapacityFor(requestedCPUs, totalIsolatedCPUs int) bool {
	return l.AvailableCPUs(totalIsolatedCPUs) >= requestedCPUs
}
