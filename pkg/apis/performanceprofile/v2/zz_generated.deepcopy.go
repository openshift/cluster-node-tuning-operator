//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
// Code generated by deepcopy-gen. DO NOT EDIT.

package v2

import (
	v1 "github.com/openshift/custom-resource-status/conditions/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CPU) DeepCopyInto(out *CPU) {
	*out = *in
	if in.Reserved != nil {
		in, out := &in.Reserved, &out.Reserved
		*out = new(CPUSet)
		**out = **in
	}
	if in.Isolated != nil {
		in, out := &in.Isolated, &out.Isolated
		*out = new(CPUSet)
		**out = **in
	}
	if in.BalanceIsolated != nil {
		in, out := &in.BalanceIsolated, &out.BalanceIsolated
		*out = new(bool)
		**out = **in
	}
	if in.Offlined != nil {
		in, out := &in.Offlined, &out.Offlined
		*out = new(CPUSet)
		**out = **in
	}
	if in.Shared != nil {
		in, out := &in.Shared, &out.Shared
		*out = new(CPUSet)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CPU.
func (in *CPU) DeepCopy() *CPU {
	if in == nil {
		return nil
	}
	out := new(CPU)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Device) DeepCopyInto(out *Device) {
	*out = *in
	if in.InterfaceName != nil {
		in, out := &in.InterfaceName, &out.InterfaceName
		*out = new(string)
		**out = **in
	}
	if in.VendorID != nil {
		in, out := &in.VendorID, &out.VendorID
		*out = new(string)
		**out = **in
	}
	if in.DeviceID != nil {
		in, out := &in.DeviceID, &out.DeviceID
		*out = new(string)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Device.
func (in *Device) DeepCopy() *Device {
	if in == nil {
		return nil
	}
	out := new(Device)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HardwareTuning) DeepCopyInto(out *HardwareTuning) {
	*out = *in
	if in.IsolatedCpuFreq != nil {
		in, out := &in.IsolatedCpuFreq, &out.IsolatedCpuFreq
		*out = new(CPUfrequency)
		**out = **in
	}
	if in.ReservedCpuFreq != nil {
		in, out := &in.ReservedCpuFreq, &out.ReservedCpuFreq
		*out = new(CPUfrequency)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HardwareTuning.
func (in *HardwareTuning) DeepCopy() *HardwareTuning {
	if in == nil {
		return nil
	}
	out := new(HardwareTuning)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HugePage) DeepCopyInto(out *HugePage) {
	*out = *in
	if in.Node != nil {
		in, out := &in.Node, &out.Node
		*out = new(int32)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HugePage.
func (in *HugePage) DeepCopy() *HugePage {
	if in == nil {
		return nil
	}
	out := new(HugePage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HugePages) DeepCopyInto(out *HugePages) {
	*out = *in
	if in.DefaultHugePagesSize != nil {
		in, out := &in.DefaultHugePagesSize, &out.DefaultHugePagesSize
		*out = new(HugePageSize)
		**out = **in
	}
	if in.Pages != nil {
		in, out := &in.Pages, &out.Pages
		*out = make([]HugePage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HugePages.
func (in *HugePages) DeepCopy() *HugePages {
	if in == nil {
		return nil
	}
	out := new(HugePages)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NUMA) DeepCopyInto(out *NUMA) {
	*out = *in
	if in.TopologyPolicy != nil {
		in, out := &in.TopologyPolicy, &out.TopologyPolicy
		*out = new(string)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NUMA.
func (in *NUMA) DeepCopy() *NUMA {
	if in == nil {
		return nil
	}
	out := new(NUMA)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Net) DeepCopyInto(out *Net) {
	*out = *in
	if in.UserLevelNetworking != nil {
		in, out := &in.UserLevelNetworking, &out.UserLevelNetworking
		*out = new(bool)
		**out = **in
	}
	if in.Devices != nil {
		in, out := &in.Devices, &out.Devices
		*out = make([]Device, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Net.
func (in *Net) DeepCopy() *Net {
	if in == nil {
		return nil
	}
	out := new(Net)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PerformanceProfile) DeepCopyInto(out *PerformanceProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PerformanceProfile.
func (in *PerformanceProfile) DeepCopy() *PerformanceProfile {
	if in == nil {
		return nil
	}
	out := new(PerformanceProfile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PerformanceProfile) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PerformanceProfileList) DeepCopyInto(out *PerformanceProfileList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PerformanceProfile, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PerformanceProfileList.
func (in *PerformanceProfileList) DeepCopy() *PerformanceProfileList {
	if in == nil {
		return nil
	}
	out := new(PerformanceProfileList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PerformanceProfileList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PerformanceProfileSpec) DeepCopyInto(out *PerformanceProfileSpec) {
	*out = *in
	if in.CPU != nil {
		in, out := &in.CPU, &out.CPU
		*out = new(CPU)
		(*in).DeepCopyInto(*out)
	}
	if in.HardwareTuning != nil {
		in, out := &in.HardwareTuning, &out.HardwareTuning
		*out = new(HardwareTuning)
		(*in).DeepCopyInto(*out)
	}
	if in.HugePages != nil {
		in, out := &in.HugePages, &out.HugePages
		*out = new(HugePages)
		(*in).DeepCopyInto(*out)
	}
	if in.MachineConfigLabel != nil {
		in, out := &in.MachineConfigLabel, &out.MachineConfigLabel
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.MachineConfigPoolSelector != nil {
		in, out := &in.MachineConfigPoolSelector, &out.MachineConfigPoolSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.RealTimeKernel != nil {
		in, out := &in.RealTimeKernel, &out.RealTimeKernel
		*out = new(RealTimeKernel)
		(*in).DeepCopyInto(*out)
	}
	if in.KernelPageSize != nil {
		in, out := &in.KernelPageSize, &out.KernelPageSize
		*out = new(KernelPageSize)
		**out = **in
	}
	if in.AdditionalKernelArgs != nil {
		in, out := &in.AdditionalKernelArgs, &out.AdditionalKernelArgs
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.NUMA != nil {
		in, out := &in.NUMA, &out.NUMA
		*out = new(NUMA)
		(*in).DeepCopyInto(*out)
	}
	if in.Net != nil {
		in, out := &in.Net, &out.Net
		*out = new(Net)
		(*in).DeepCopyInto(*out)
	}
	if in.GloballyDisableIrqLoadBalancing != nil {
		in, out := &in.GloballyDisableIrqLoadBalancing, &out.GloballyDisableIrqLoadBalancing
		*out = new(bool)
		**out = **in
	}
	if in.WorkloadHints != nil {
		in, out := &in.WorkloadHints, &out.WorkloadHints
		*out = new(WorkloadHints)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PerformanceProfileSpec.
func (in *PerformanceProfileSpec) DeepCopy() *PerformanceProfileSpec {
	if in == nil {
		return nil
	}
	out := new(PerformanceProfileSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PerformanceProfileStatus) DeepCopyInto(out *PerformanceProfileStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Tuned != nil {
		in, out := &in.Tuned, &out.Tuned
		*out = new(string)
		**out = **in
	}
	if in.RuntimeClass != nil {
		in, out := &in.RuntimeClass, &out.RuntimeClass
		*out = new(string)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PerformanceProfileStatus.
func (in *PerformanceProfileStatus) DeepCopy() *PerformanceProfileStatus {
	if in == nil {
		return nil
	}
	out := new(PerformanceProfileStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RealTimeKernel) DeepCopyInto(out *RealTimeKernel) {
	*out = *in
	if in.Enabled != nil {
		in, out := &in.Enabled, &out.Enabled
		*out = new(bool)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RealTimeKernel.
func (in *RealTimeKernel) DeepCopy() *RealTimeKernel {
	if in == nil {
		return nil
	}
	out := new(RealTimeKernel)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *WorkloadHints) DeepCopyInto(out *WorkloadHints) {
	*out = *in
	if in.HighPowerConsumption != nil {
		in, out := &in.HighPowerConsumption, &out.HighPowerConsumption
		*out = new(bool)
		**out = **in
	}
	if in.RealTime != nil {
		in, out := &in.RealTime, &out.RealTime
		*out = new(bool)
		**out = **in
	}
	if in.PerPodPowerManagement != nil {
		in, out := &in.PerPodPowerManagement, &out.PerPodPowerManagement
		*out = new(bool)
		**out = **in
	}
	if in.MixedCpus != nil {
		in, out := &in.MixedCpus, &out.MixedCpus
		*out = new(bool)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new WorkloadHints.
func (in *WorkloadHints) DeepCopy() *WorkloadHints {
	if in == nil {
		return nil
	}
	out := new(WorkloadHints)
	in.DeepCopyInto(out)
	return out
}
