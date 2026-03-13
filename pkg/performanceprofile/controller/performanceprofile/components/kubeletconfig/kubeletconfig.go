package kubeletconfig

import (
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
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
	experimentalKubeletSnippetAnnotation         = "kubeletconfig.experimental"
	cpuManagerPolicyStatic                       = "static"
	cpuManagerPolicyOptionFullPCPUsOnly          = "full-pcpus-only"
	memoryManagerPolicyStatic                    = "Static"
	defaultKubeReservedMemory                    = "500Mi"
	defaultSystemReservedMemory                  = "500Mi"
	defaultHardEvictionThresholdMemory           = "100Mi"
	defaultHardEvictionThresholdNodefs           = "10%"
	defaultHardEvictionThresholdImagefs          = "15%"
	defaultHardEvictionThresholdNodefsInodesFree = "5%"
	evictionHardMemoryAvailable                  = "memory.available"
	evictionHardNodefsAvaialble                  = "nodefs.available"
	evictionHardImagefsAvailable                 = "imagefs.available"
	evictionHardNodefsInodesFree                 = "nodefs.inodesFree"
)

// New returns new KubeletConfig object for performance sensitive workflows
func New(profile *performancev2.PerformanceProfile, opts *components.KubeletConfigOptions) (*machineconfigv1.KubeletConfig, error) {
	if err := validateOptions(opts); err != nil {
		return nil, fmt.Errorf("KubeletConfig options validation failed: %w", err)
	}

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

	// when DRA resource management is enabled, all kubeletconfig settings should be disabled.
	// this is because the DRA plugin will manage the resource allocation.
	// if the kubeletconfig CPU and Memory Manager settings are not disabled, it will conflict with the DRA.
	if opts.DRAResourceManagement {
		if err := setKubeletConfigForDRAManagement(kubeletConfig, opts); err != nil {
			return nil, err
		}
	} else {
		if err := setKubeletConfigForCPUAndMemoryManagers(profile, kubeletConfig, opts); err != nil {
			return nil, err
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
				MatchLabels: opts.MachineConfigPoolSelector,
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

func setKubeletConfigForDRAManagement(kubeletConfig *kubeletconfigv1beta1.KubeletConfiguration, opts *components.KubeletConfigOptions) error {
	kubeletConfig.CPUManagerPolicy = "none"
	kubeletConfig.CPUManagerPolicyOptions = map[string]string{}
	kubeletConfig.TopologyManagerPolicy = kubeletconfigv1beta1.NoneTopologyManagerPolicy
	kubeletConfig.MemoryManagerPolicy = kubeletconfigv1beta1.NoneMemoryManagerPolicy
	return nil
}

func setKubeletConfigForCPUAndMemoryManagers(profile *performancev2.PerformanceProfile, kubeletConfig *kubeletconfigv1beta1.KubeletConfiguration, opts *components.KubeletConfigOptions) error {
	kubeletConfig.CPUManagerPolicy = cpuManagerPolicyStatic
	kubeletConfig.CPUManagerReconcilePeriod = metav1.Duration{Duration: 5 * time.Second}
	kubeletConfig.TopologyManagerPolicy = kubeletconfigv1beta1.BestEffortTopologyManagerPolicy

	// set the default hard eviction memory threshold
	if kubeletConfig.EvictionHard == nil {
		kubeletConfig.EvictionHard = map[string]string{}
	}
	if _, ok := kubeletConfig.EvictionHard[evictionHardMemoryAvailable]; !ok {
		kubeletConfig.EvictionHard[evictionHardMemoryAvailable] = defaultHardEvictionThresholdMemory
	}
	if _, ok := kubeletConfig.EvictionHard[evictionHardNodefsAvaialble]; !ok {
		kubeletConfig.EvictionHard[evictionHardNodefsAvaialble] = defaultHardEvictionThresholdNodefs
	}
	if _, ok := kubeletConfig.EvictionHard[evictionHardImagefsAvailable]; !ok {
		kubeletConfig.EvictionHard[evictionHardImagefsAvailable] = defaultHardEvictionThresholdImagefs
	}
	if _, ok := kubeletConfig.EvictionHard[evictionHardNodefsInodesFree]; !ok {
		kubeletConfig.EvictionHard[evictionHardNodefsInodesFree] = defaultHardEvictionThresholdNodefsInodesFree
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

	if opts.MixedCPUsEnabled {
		sharedCPUs, err := cpuset.Parse(string(*profile.Spec.CPU.Shared))
		if err != nil {
			return err
		}
		reservedCPUs, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
		if err != nil {
			return err
		}
		kubeletConfig.ReservedSystemCPUs = reservedCPUs.Union(sharedCPUs).String()
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
						return err
					}
					if err := addStringToQuantity(reservedMemory, kubeletConfig.SystemReserved[string(corev1.ResourceMemory)]); err != nil {
						return err
					}
					if err := addStringToQuantity(reservedMemory, kubeletConfig.EvictionHard[evictionHardMemoryAvailable]); err != nil {
						return err
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
	return nil
}
func validateOptions(opts *components.KubeletConfigOptions) error {
	if opts == nil {
		return nil
	}

	if opts.MixedCPUsEnabled && opts.DRAResourceManagement {
		return fmt.Errorf("invalid configuration: mixed CPUs mode and DRA resource management features are mutually exclusive. please disable one of the features before continuing")
	}

	return nil
}
