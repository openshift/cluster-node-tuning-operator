package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TODO: handle init containers vs application containers

func TotalCPUsRoundedUp(containersResources []corev1.ResourceList) int {
	totalCPUs := *resource.NewQuantity(0, resource.DecimalSI)
	for _, containerResources := range containersResources {
		totalCPUs.Add(*containerResources.Cpu())
	}
	return int(totalCPUs.Value())
}

func MaxCPURequestsRoundedUp(containersResources []corev1.ResourceList) int {
	maxPodCpus := 0
	for _, containerResources := range containersResources {
		current := int(containerResources.Cpu().Value())
		if current > maxPodCpus {
			maxPodCpus = current
		}
	}
	return maxPodCpus
}
