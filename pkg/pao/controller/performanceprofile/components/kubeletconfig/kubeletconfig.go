package kubeletconfig

import (
	"encoding/json"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"

	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	// experimentalKubeletSnippetAnnotation contains the annotation key that should be used to provide a KubeletConfig snippet with additional
	// configurations you want to apply on top of the generated KubeletConfig resource.
	// To find the specific argument see https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/.
	// By default, the performance-addon-operator will override:
	// 1. CPU manager policy
	// 2. CPU manager reconcile period
	// 3. Topology manager policy
	// 4. Reserved CPUs
	// 5. Memory manager policy
	// Please avoid specifying them and use the relevant API to configure these parameters.
	experimentalKubeletSnippetAnnotation = "kubeletconfig.experimental"
	cpuManagerPolicyStatic               = "static"
	cpuManagerPolicyOptionFullPCPUsOnly  = "full-pcpus-only"
	memoryManagerPolicyStatic            = "Static"
	defaultKubeReservedMemory            = "500Mi"
	defaultSystemReservedMemory          = "500Mi"
	defaultHardEvictionThreshold         = "100Mi"
	evictionHardMemoryAvailable          = "memory.available"
)

// New returns new KubeletConfig object for performance sensetive workflows
func New(profile *performancev2.PerformanceProfile, profileMCPLabels map[string]string) (*machineconfigv1.KubeletConfig, error) {
	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	kubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	if v, ok := profile.Annotations[experimentalKubeletSnippetAnnotation]; ok {
		if err := json.Unmarshal([]byte(v), kubeletConfig); err != nil {
			return nil, err
		}
	}

	kubeletConfig.TypeMeta = metav1.TypeMeta{
		APIVersion: kubeletconfigv1beta1.SchemeGroupVersion.String(),
		Kind:       "KubeletConfiguration",
	}

	kubeletConfig.CPUManagerPolicy = cpuManagerPolicyStatic
	kubeletConfig.CPUManagerReconcilePeriod = metav1.Duration{Duration: 5 * time.Second}
	kubeletConfig.TopologyManagerPolicy = kubeletconfigv1beta1.BestEffortTopologyManagerPolicy

	// set the default hard eviction memory threshold
	if kubeletConfig.EvictionHard == nil {
		kubeletConfig.EvictionHard = map[string]string{}
	}
	if _, ok := kubeletConfig.EvictionHard[evictionHardMemoryAvailable]; !ok {
		kubeletConfig.EvictionHard[evictionHardMemoryAvailable] = defaultHardEvictionThreshold
	}

	// set the default memory kube-reserved
	if kubeletConfig.KubeReserved == nil {
		kubeletConfig.KubeReserved = map[string]string{}
	}
	if _, ok := kubeletConfig.KubeReserved[string(corev1.ResourceMemory)]; !ok {
		kubeletConfig.KubeReserved[string(corev1.ResourceMemory)] = defaultKubeReservedMemory
	}

	// set the default memory system-reserved
	if kubeletConfig.SystemReserved == nil {
		kubeletConfig.SystemReserved = map[string]string{}
	}
	if _, ok := kubeletConfig.SystemReserved[string(corev1.ResourceMemory)]; !ok {
		kubeletConfig.SystemReserved[string(corev1.ResourceMemory)] = defaultSystemReservedMemory
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

				if kubeletConfig.ReservedMemory == nil {
					reservedMemory := resource.NewQuantity(0, resource.DecimalSI)
					if err := addStringToQuantity(reservedMemory, kubeletConfig.KubeReserved[string(corev1.ResourceMemory)]); err != nil {
						return nil, err
					}
					if err := addStringToQuantity(reservedMemory, kubeletConfig.SystemReserved[string(corev1.ResourceMemory)]); err != nil {
						return nil, err
					}
					if err := addStringToQuantity(reservedMemory, kubeletConfig.EvictionHard[evictionHardMemoryAvailable]); err != nil {
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

				// require full physical CPUs only to ensure maximum isolation
				if topologyPolicy == kubeletconfigv1beta1.SingleNumaNodeTopologyManagerPolicy {
					if kubeletConfig.CPUManagerPolicyOptions == nil {
						kubeletConfig.CPUManagerPolicyOptions = make(map[string]string)
					}

					if _, ok := kubeletConfig.CPUManagerPolicyOptions[cpuManagerPolicyOptionFullPCPUsOnly]; !ok {
						kubeletConfig.CPUManagerPolicyOptions[cpuManagerPolicyOptionFullPCPUsOnly] = "true"
					}
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
