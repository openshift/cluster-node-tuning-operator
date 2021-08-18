package kubeletconfig

import (
	"encoding/json"
	"time"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

const (
	cpuManagerPolicyStatic       = "static"
	memoryManagerPolicyStatic    = "Static"
	defaultKubeReservedCPU       = "1000m"
	defaultKubeReservedMemory    = "500Mi"
	defaultSystemReservedCPU     = "1000m"
	defaultSystemReservedMemory  = "500Mi"
	defaultHardEvictionThreshold = "100Mi"
)

// New returns new KubeletConfig object for performance sensetive workflows
func New(profile *performancev2.PerformanceProfile, profileMCPLabels map[string]string) (*machineconfigv1.KubeletConfig, error) {
	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	kubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kubelet.config.k8s.io/v1beta1",
			Kind:       "KubeletConfiguration",
		},
		CPUManagerPolicy:          cpuManagerPolicyStatic,
		CPUManagerReconcilePeriod: metav1.Duration{Duration: 5 * time.Second},
		TopologyManagerPolicy:     kubeletconfigv1beta1.BestEffortTopologyManagerPolicy,
		EvictionHard: map[string]string{
			"memory.available": defaultHardEvictionThreshold,
		},
		KubeReserved: map[string]string{
			"cpu":    defaultKubeReservedCPU,
			"memory": defaultKubeReservedMemory,
		},
		SystemReserved: map[string]string{
			"cpu":    defaultSystemReservedCPU,
			"memory": defaultSystemReservedMemory,
		},
	}

	if profile.Spec.CPU != nil && profile.Spec.CPU.Reserved != nil {
		kubeletConfig.ReservedSystemCPUs = string(*profile.Spec.CPU.Reserved)
	}

	if profile.Spec.NUMA != nil {
		if profile.Spec.NUMA.TopologyPolicy != nil {
			topologyPolicy := *profile.Spec.NUMA.TopologyPolicy
			kubeletConfig.TopologyManagerPolicy = topologyPolicy

			// set the memory manager policy to static only when the topology policy is
			// restricted or single NUMA node
			if topologyPolicy == kubeletconfigv1beta1.RestrictedTopologyManagerPolicy ||
				topologyPolicy == kubeletconfigv1beta1.SingleNumaNodeTopologyManagerPolicy {
				kubeletConfig.MemoryManagerPolicy = memoryManagerPolicyStatic

				reservedMemory := resource.NewQuantity(0, resource.DecimalSI)
				if err := addStringToQuantity(reservedMemory, defaultKubeReservedMemory); err != nil {
					return nil, err
				}
				if err := addStringToQuantity(reservedMemory, defaultSystemReservedMemory); err != nil {
					return nil, err
				}
				if err := addStringToQuantity(reservedMemory, defaultHardEvictionThreshold); err != nil {
					return nil, err
				}

				kubeletConfig.ReservedMemory = []kubeletconfigv1beta1.MemoryReservation{
					{
						// the NUMA node 0 is the only safe choice for non NUMA machines
						//  in the future we can extend our API to get this information from a user
						NumaNode: 0,
						Limits: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceMemory: *reservedMemory,
						},
					},
				}
			}
		}
	}

	raw, err := json.Marshal(kubeletConfig)
	if err != nil {
		return nil, err
	}

	return &machineconfigv1.KubeletConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind:       "KubeletConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: machineconfigv1.KubeletConfigSpec{
			MachineConfigPoolSelector: &metav1.LabelSelector{
				MatchLabels: profileMCPLabels,
			},
			KubeletConfig: &runtime.RawExtension{
				Raw: raw,
			},
		},
	}, nil
}

func addStringToQuantity(q *resource.Quantity, value string) error {
	v, err := resource.ParseQuantity(value)
	if err != nil {
		return err
	}
	q.Add(v)

	return nil
}
