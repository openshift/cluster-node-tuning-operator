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

package v2

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

const (
	hugepagesSize2M   = "2M"
	hugepagesSize32M  = "32M"
	hugepagesSize512M = "512M"
	hugepagesSize1G   = "1G"
	amd64             = "amd64"
	aarch64           = "arm64"
)

var x86ValidHugepagesSizes = []string{
	hugepagesSize2M,
	hugepagesSize1G,
}

// Each kernel page size has only a single valid hugepage size on aarch64
var aarch64ValidHugepagesSizes = []string{
	hugepagesSize2M,   // With 4k kernel pages
	hugepagesSize32M,  // With 16k kernel pages
	hugepagesSize512M, // With 64k kernel pages
}

var validatorContext = context.TODO()

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateCreate() (admission.Warnings, error) {
	klog.Infof("Create validation for the performance profile %q", r.Name)

	return r.validateCreateOrUpdate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	klog.Infof("Update validation for the performance profile %q", r.Name)

	return r.validateCreateOrUpdate()
}

func (r *PerformanceProfile) validateCreateOrUpdate() (admission.Warnings, error) {
	var allErrs field.ErrorList

	// validate node selector duplication
	ppList := &PerformanceProfileList{}
	if err := validatorClient.List(validatorContext, ppList); err != nil {
		return admission.Warnings{}, apierrors.NewInternalError(err)
	}

	allErrs = append(allErrs, r.validateNodeSelectorDuplication(ppList)...)

	// validate basic fields
	allErrs = append(allErrs, r.ValidateBasicFields()...)

	if len(allErrs) == 0 {
		return admission.Warnings{}, nil
	}

	return admission.Warnings{}, apierrors.NewInvalid(
		schema.GroupKind{Group: "performance.openshift.io", Kind: "PerformanceProfile"},
		r.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateDelete() (admission.Warnings, error) {
	klog.Infof("Delete validation for the performance profile %q", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return admission.Warnings{}, nil
}

func (r *PerformanceProfile) validateNodeSelectorDuplication(ppList *PerformanceProfileList) field.ErrorList {
	var allErrs field.ErrorList

	// validate node selector duplication
	for _, pp := range ppList.Items {
		// exclude the current profile from the check
		if pp.Name == r.Name {
			continue
		}

		if reflect.DeepEqual(pp.Spec.NodeSelector, r.Spec.NodeSelector) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.nodeSelector"), r.Spec.NodeSelector, fmt.Sprintf("the profile has the same node selector as the performance profile %q", pp.Name)))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) ValidateBasicFields() field.ErrorList {
	var allErrs field.ErrorList

	nodes, err := r.getNodesList()
	if err != nil {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec.nodeSelector"),
				r.Spec.NodeSelector,
				err.Error(),
			),
		)
	}

	allErrs = append(allErrs, r.validateCPUs()...)
	allErrs = append(allErrs, r.validateSelectors()...)
	allErrs = append(allErrs, r.validateAllNodesAreSameCpuArchitecture(nodes)...)
	allErrs = append(allErrs, r.validateAllNodesAreSameCpuCapacity(nodes)...)
	allErrs = append(allErrs, r.validateHugePages(nodes)...)
	allErrs = append(allErrs, r.validateNUMA()...)
	allErrs = append(allErrs, r.validateNet()...)
	allErrs = append(allErrs, r.validateWorkloadHints()...)
	allErrs = append(allErrs, r.validateCpuFrequency()...)

	return allErrs
}

func (r *PerformanceProfile) validateCPUs() field.ErrorList {
	var allErrs field.ErrorList
	// shortcut
	cpus := r.Spec.CPU
	if cpus == nil {
		allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu"), "cpu section required"))
	} else {
		if cpus.Isolated == nil {
			allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu.isolated"), "isolated CPUs required"))
		}

		if cpus.Reserved == nil {
			allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu.reserved"), "reserved CPUs required"))
		}

		if cpus.Isolated != nil && cpus.Reserved != nil {
			var offlined, shared string
			if cpus.Offlined != nil {
				offlined = string(*cpus.Offlined)
			}
			if cpus.Shared != nil {
				shared = string(*cpus.Shared)
			}
			cpuLists, err := components.NewCPULists(string(*cpus.Reserved), string(*cpus.Isolated), offlined, shared)
			if err != nil {
				allErrs = append(allErrs, field.InternalError(field.NewPath("spec.cpu"), err))
			}

			if cpuLists.GetReserved().IsEmpty() {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu.reserved"), cpus.Reserved, "reserved CPUs can not be empty"))
			}

			if cpuLists.GetIsolated().IsEmpty() {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu.isolated"), cpus.Isolated, "isolated CPUs can not be empty"))
			}

			allErrs = validateNoIntersectionExists(cpuLists, allErrs)
		}
	}
	return allErrs
}

// validateNoIntersectionExists iterates over the provided CPU lists and validates that
// none of the lists are intersected with each other.
func validateNoIntersectionExists(lists *components.CPULists, allErrs field.ErrorList) field.ErrorList {
	for k1, cpuset1 := range lists.GetSets() {
		for k2, cpuset2 := range lists.GetSets() {
			if k1 == k2 {
				continue
			}
			if overlap := components.Intersect(cpuset1, cpuset2); len(overlap) != 0 {
				allErrs = append(allErrs, field.Forbidden(field.NewPath("spec.cpu"), fmt.Sprintf("%s and %s cpus overlap: %v", k1, k2, overlap)))
			}
		}
	}
	return allErrs
}

func (r *PerformanceProfile) validateSelectors() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.MachineConfigLabel != nil && len(r.Spec.MachineConfigLabel) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.machineConfigLabel"), r.Spec.MachineConfigLabel, "you should provide only 1 MachineConfigLabel"))
	}

	if r.Spec.MachineConfigPoolSelector != nil && len(r.Spec.MachineConfigPoolSelector) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.machineConfigPoolSelector"), r.Spec.MachineConfigLabel, "you should provide only 1 MachineConfigPoolSelector"))
	}

	if r.Spec.NodeSelector == nil {
		allErrs = append(allErrs, field.Required(field.NewPath("spec.nodeSelector"), "the nodeSelector required"))
	}

	if len(r.Spec.NodeSelector) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.nodeSelector"), r.Spec.NodeSelector, "you should provide ony 1 NodeSelector"))
	}

	// in case MachineConfigLabels or MachineConfigPoolSelector are not set, we expect a certain format (domain/role)
	// on the NodeSelector in order to be able to calculate the default values for the former metioned fields.
	if r.Spec.MachineConfigLabel == nil || r.Spec.MachineConfigPoolSelector == nil {
		k, _ := components.GetFirstKeyAndValue(r.Spec.NodeSelector)
		if _, _, err := components.SplitLabelKey(k); err != nil {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.nodeSelector"),
				r.Spec.NodeSelector,
				"machineConfigLabels or machineConfigPoolSelector are not set, but we can not set it automatically because of an invalid NodeSelector label key that can't be split into domain/role"))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateAllNodesAreSameCpuArchitecture(nodes corev1.NodeList) field.ErrorList {
	var allErrs field.ErrorList
	// First check if the node list has valid elements
	if len(nodes.Items) == 0 {
		// We are unable to validate this if we have no nodes
		// But no nodes is still a valid profile so skip this validation
		return allErrs
	}

	// We need to use one of the nodes as a reference for comparing against the rest
	// The first item in the list is simple and easy to use
	expectedArchitecture := getCpuArchitectureForNode(nodes.Items[0])

	if expectedArchitecture == "" {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec.nodeSelector"),
				r.Spec.NodeSelector,
				fmt.Sprintf("Failed to detect architecture for node %s", nodes.Items[0].Status.NodeInfo.MachineID),
			),
		)

		// If we failed to detect cpu architecture there is not much point to continue
		// We would likely just get an error for every single node with the same error
		return allErrs
	}

	// Make sure all other nodes have the same value
	for i := 1; i < len(nodes.Items); i++ {
		actualArchitecture := getCpuArchitectureForNode(nodes.Items[i])
		if actualArchitecture != expectedArchitecture {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec.nodeSelector"),
					r.Spec.NodeSelector,
					fmt.Sprintf("Node %s has architecture %s but was expecting %s", nodes.Items[i].Status.NodeInfo.MachineID, actualArchitecture, expectedArchitecture),
				),
			)
		}
	}

	return allErrs
}

func getCpuArchitectureForNode(node corev1.Node) string {
	return node.Status.NodeInfo.Architecture
}

func (r *PerformanceProfile) validateAllNodesAreSameCpuCapacity(nodes corev1.NodeList) field.ErrorList {
	var allErrs field.ErrorList
	// First check if the node list has valid elements
	if len(nodes.Items) == 0 {
		// We are unable to validate this if we have no nodes
		// But no nodes is still a valid profile so skip this validation
		return allErrs
	}

	// We need to use one of the nodes as a reference for comparing against the rest
	// The first item in the list is simple and easy to use
	expectedCpuCapacity := getCpuCapacityForNode(nodes.Items[0])

	if expectedCpuCapacity == "" {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec.nodeSelector"),
				r.Spec.NodeSelector,
				fmt.Sprintf("Failed to detect cpu capacity for node %s", nodes.Items[0].Status.NodeInfo.MachineID),
			),
		)

		// If we failed to detect cpu capacity there is not much point to continue
		// We would likely just get an error for every single node with the same error
		return allErrs
	}

	// Make sure all other nodes have the same value
	for i := 1; i < len(nodes.Items); i++ {
		actualCpuCapacity := getCpuCapacityForNode(nodes.Items[i])
		if actualCpuCapacity != expectedCpuCapacity {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec.nodeSelector"),
					r.Spec.NodeSelector,
					fmt.Sprintf("Node %s has CPU capacity %s but was expecting %s", nodes.Items[i].Status.NodeInfo.MachineID, actualCpuCapacity, expectedCpuCapacity),
				),
			)
		}
	}

	return allErrs
}

func getCpuCapacityForNode(node corev1.Node) string {
	return node.Status.Capacity.Cpu().String()
}

func (r *PerformanceProfile) validateHugePages(nodes corev1.NodeList) field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.HugePages == nil {
		return allErrs
	}

	// We can only partially validate this if we have no nodes
	// We can check that the value used is legitimate but we cannot check
	// whether it is supposed to be x86 or aarch64
	x86 := false
	aarch64 := false
	combinedHugepagesSizes := append(x86ValidHugepagesSizes, aarch64ValidHugepagesSizes...)

	if len(nodes.Items) > 0 {
		// `validateHugePages` implicitly relies on `validateAllNodesAreSameCpuArchitecture` to have already been run
		// Under that assumption we can return any node from the list since they should all be the same architecture
		// However it is simple and easy to just return the first node
		x86 = isX86(nodes.Items[0])
		aarch64 = isAarch64(nodes.Items[0])
	}

	if r.Spec.HugePages.DefaultHugePagesSize != nil {
		defaultSize := *r.Spec.HugePages.DefaultHugePagesSize
		errField := "spec.hugepages.defaultHugepagesSize"
		errMsg := "hugepages default size should be equal to one of"

		if x86 && !slices.Contains(x86ValidHugepagesSizes, string(defaultSize)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.DefaultHugePagesSize,
					fmt.Sprintf("%s %v", errMsg, x86ValidHugepagesSizes),
				),
			)
		} else if aarch64 && !slices.Contains(aarch64ValidHugepagesSizes, string(defaultSize)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.DefaultHugePagesSize,
					fmt.Sprintf("%s %v", errMsg, aarch64ValidHugepagesSizes),
				),
			)
		} else if !x86 && !aarch64 && !slices.Contains(combinedHugepagesSizes, string(defaultSize)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.DefaultHugePagesSize,
					fmt.Sprintf("%s %v", errMsg, combinedHugepagesSizes),
				),
			)
		}
	}

	for i, page := range r.Spec.HugePages.Pages {
		errField := "spec.hugepages.pages"
		errMsg := "the page size should be equal to one of"
		if x86 && !slices.Contains(x86ValidHugepagesSizes, string(page.Size)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.Pages,
					fmt.Sprintf("%s %v", errMsg, x86ValidHugepagesSizes),
				),
			)
		} else if aarch64 && !slices.Contains(aarch64ValidHugepagesSizes, string(page.Size)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.Pages,
					fmt.Sprintf("%s %v", errMsg, aarch64ValidHugepagesSizes),
				),
			)
		} else if !x86 && !aarch64 && !slices.Contains(combinedHugepagesSizes, string(page.Size)) {
			allErrs = append(
				allErrs,
				field.Invalid(
					field.NewPath(errField),
					r.Spec.HugePages.DefaultHugePagesSize,
					fmt.Sprintf("%s %v", errMsg, combinedHugepagesSizes),
				),
			)
		}

		allErrs = append(allErrs, r.validatePageDuplication(&page, r.Spec.HugePages.Pages[i+1:])...)
	}

	return allErrs
}

func isX86(node corev1.Node) bool {
	return getCpuArchitectureForNode(node) == amd64
}

func isAarch64(node corev1.Node) bool {
	return getCpuArchitectureForNode(node) == aarch64
}

func (r *PerformanceProfile) validatePageDuplication(page *HugePage, pages []HugePage) field.ErrorList {
	var allErrs field.ErrorList

	for _, p := range pages {
		if page.Size != p.Size {
			continue
		}

		if page.Node != nil && p.Node == nil {
			continue
		}

		if page.Node == nil && p.Node != nil {
			continue
		}

		if page.Node == nil && p.Node == nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.pages"), r.Spec.HugePages.Pages, fmt.Sprintf("the page with the size %q and without the specified NUMA node, has duplication", page.Size)))
			continue
		}

		if *page.Node == *p.Node {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.pages"), r.Spec.HugePages.Pages, fmt.Sprintf("the page with the size %q and with specified NUMA node %d, has duplication", page.Size, *page.Node)))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateNUMA() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.NUMA == nil {
		return allErrs
	}

	// validate NUMA topology policy matches allowed values
	if r.Spec.NUMA.TopologyPolicy != nil {
		policy := *r.Spec.NUMA.TopologyPolicy
		if policy != kubeletconfigv1beta1.NoneTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.BestEffortTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.RestrictedTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.SingleNumaNodeTopologyManagerPolicy {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.numa.topologyPolicy"), r.Spec.NUMA.TopologyPolicy, "unrecognized value for topologyPolicy"))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateNet() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.Net == nil {
		return allErrs
	}

	if r.Spec.Net.UserLevelNetworking != nil && *r.Spec.Net.UserLevelNetworking && r.Spec.CPU.Reserved == nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net"), r.Spec.Net, "can not set network devices queues count without specifying spec.cpu.reserved"))
	}

	for _, device := range r.Spec.Net.Devices {
		if device.InterfaceName != nil && *device.InterfaceName == "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, "device name cannot be empty"))
		}
		if device.VendorID != nil && !isValid16bitsHexID(*device.VendorID) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, fmt.Sprintf("device vendor ID %s has an invalid format. Vendor ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", *device.VendorID)))
		}
		if device.DeviceID != nil && !isValid16bitsHexID(*device.DeviceID) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, fmt.Sprintf("device model ID %s has an invalid format. Model ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", *device.DeviceID)))
		}
		if device.DeviceID != nil && device.VendorID == nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, "device model ID can not be used without specifying the device vendor ID."))
		}
	}
	return allErrs
}

func isValid16bitsHexID(v string) bool {
	re := regexp.MustCompile("^0x[0-9a-fA-F]+$")
	return re.MatchString(v) && len(v) < 7
}

func (r *PerformanceProfile) validateWorkloadHints() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.WorkloadHints == nil {
		return allErrs
	}

	if r.Spec.RealTimeKernel != nil {
		if r.Spec.RealTimeKernel.Enabled != nil && *r.Spec.RealTimeKernel.Enabled {
			if r.Spec.WorkloadHints.RealTime != nil && !*r.Spec.WorkloadHints.RealTime {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.workloadHints.realTime"), r.Spec.WorkloadHints.RealTime, "realtime kernel is enabled, but realtime workload hint is explicitly disable"))
			}
		}
	}

	if r.Spec.WorkloadHints.HighPowerConsumption != nil && *r.Spec.WorkloadHints.HighPowerConsumption {
		if r.Spec.WorkloadHints.PerPodPowerManagement != nil && *r.Spec.WorkloadHints.PerPodPowerManagement {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.workloadHints.HighPowerConsumption"), r.Spec.WorkloadHints.HighPowerConsumption, "Invalid WorkloadHints configuration: HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
		}
	}

	if r.Spec.WorkloadHints.MixedCpus != nil && *r.Spec.WorkloadHints.MixedCpus {
		if r.Spec.CPU.Shared == nil || *r.Spec.CPU.Shared == "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.workloadHints.MixedCpus"), r.Spec.WorkloadHints.MixedCpus, "Invalid WorkloadHints configuration: MixedCpus enabled but no shared CPUs were specified"))
		}
	}
	return allErrs
}

func (r *PerformanceProfile) validateCpuFrequency() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.HardwareTuning != nil {
		if r.Spec.HardwareTuning.IsolatedCpuFreq != nil && r.Spec.HardwareTuning.ReservedCpuFreq != nil {
			isolatedFreq := *r.Spec.HardwareTuning.IsolatedCpuFreq
			if isolatedFreq == 0 {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hardwareTuning.isolatedCpuFreq"), r.Spec.HardwareTuning.IsolatedCpuFreq, "isolated cpu frequency can not be equal to 0"))
			}

			reservedFreq := *r.Spec.HardwareTuning.ReservedCpuFreq
			if reservedFreq == 0 {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hardwareTuning.reservedCpuFreq"), r.Spec.HardwareTuning.ReservedCpuFreq, "reserved cpu frequency can not be equal to 0"))
			}
		} else {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hardwareTuning"), r.Spec.HardwareTuning, "both isolated and reserved cpu frequency must be declared"))
		}
		return allErrs
	}

	return allErrs
}

func (r *PerformanceProfile) getNodesList() (corev1.NodeList, error) {
	// Get the nodes from the client using the node selector in the profile
	nodes := &corev1.NodeList{}

	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: r.Spec.NodeSelector,
	})

	if err != nil {
		return corev1.NodeList{}, err
	}

	err = validatorClient.List(validatorContext, nodes, &client.ListOptions{
		LabelSelector: selector,
	})

	if err != nil {
		return corev1.NodeList{}, err
	}

	return *nodes, nil
}
