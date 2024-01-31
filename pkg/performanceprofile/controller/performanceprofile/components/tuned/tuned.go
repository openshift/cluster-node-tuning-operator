package tuned

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"

	assets "github.com/openshift/cluster-node-tuning-operator/assets/performanceprofile"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
)

const (
	cmdlineDelimiter                        = " "
	templateIsolatedCpus                    = "IsolatedCpus"
	templateStaticIsolation                 = "StaticIsolation"
	templateDefaultHugepagesSize            = "DefaultHugepagesSize"
	templateHugepages                       = "Hugepages"
	templateAdditionalArgs                  = "AdditionalArgs"
	templateGloballyDisableIrqLoadBalancing = "GloballyDisableIrqLoadBalancing"
	templateNetDevices                      = "NetDevices"
	nfConntrackHashsize                     = "nf_conntrack_hashsize=131072"
	templateRealTimeHint                    = "RealTimeHint"
	templateHighPowerConsumption            = "HighPowerConsumption"
	templatePerPodPowerManagement           = "PerPodPowerManagement"
	templateHardwareTuning                  = "HardwareTuning"
	templateIsolatedCpuMaxFreq              = "IsolatedCpuMaxFreq"
	templateReservedCpuMaxFreq              = "ReservedCpuMaxFreq"
	templateIsolatedCpuList                 = "IsolatedCpuList"
	templateReservedCpuList                 = "ReservedCpuList"
)

func new(name string, profiles []tunedv1.TunedProfile, recommends []tunedv1.TunedRecommend) *tunedv1.Tuned {
	return &tunedv1.Tuned{
		TypeMeta: metav1.TypeMeta{
			APIVersion: tunedv1.SchemeGroupVersion.String(),
			Kind:       "Tuned",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: components.NamespaceNodeTuningOperator,
		},
		Spec: tunedv1.TunedSpec{
			Profile:   profiles,
			Recommend: recommends,
		},
	}
}

// NewNodePerformance returns tuned profile for performance sensitive workflows
func NewNodePerformance(profile *performancev2.PerformanceProfile) (*tunedv1.Tuned, error) {
	templateArgs := make(map[string]interface{})

	if profile.Spec.CPU.Isolated != nil {
		templateArgs[templateIsolatedCpus] = string(*profile.Spec.CPU.Isolated)
		if profile.Spec.CPU.BalanceIsolated != nil && !*profile.Spec.CPU.BalanceIsolated {
			templateArgs[templateStaticIsolation] = strconv.FormatBool(true)
		}
	}

	if profile.Spec.HardwareTuning != nil {
		isolatedCpuSet, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
		if err != nil {
			return nil, err
		}
		isolatedCpuString := components.ListToString(isolatedCpuSet.List())
		// converts a string to a string slice
		isolatedCpuList := strings.SplitN(isolatedCpuString, ",", len(isolatedCpuString))

		reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
		if err != nil {
			return nil, err
		}
		reservedCpuString := components.ListToString(reservedSet.List())
		// converts a string to a string slice
		reservedCpuList := strings.SplitN(reservedCpuString, ",", len(reservedCpuString))

		templateArgs[templateHardwareTuning] = strconv.FormatBool(true)
		templateArgs[templateIsolatedCpuList] = isolatedCpuList
		templateArgs[templateReservedCpuList] = reservedCpuList
		templateArgs[templateIsolatedCpuMaxFreq] = int(*profile.Spec.HardwareTuning.IsolatedCpuFreq)
		templateArgs[templateReservedCpuMaxFreq] = int(*profile.Spec.HardwareTuning.ReservedCpuFreq)
	}

	if profile.Spec.HugePages != nil {
		var defaultHugepageSize performancev2.HugePageSize
		if profile.Spec.HugePages.DefaultHugePagesSize != nil {
			defaultHugepageSize = *profile.Spec.HugePages.DefaultHugePagesSize
			templateArgs[templateDefaultHugepagesSize] = string(defaultHugepageSize)
		}

		var is2MHugepagesRequested *bool
		var hugepages []string
		for _, page := range profile.Spec.HugePages.Pages {
			// we can not allocate huge pages on the specific NUMA node via kernel boot arguments
			if page.Node != nil {
				// a user requested to allocate 2M huge pages on the specific NUMA node,
				// append dummy kernel arguments
				if page.Size == components.HugepagesSize2M && is2MHugepagesRequested == nil {
					is2MHugepagesRequested = pointer.Bool(true)
				}
				continue
			}

			// a user requested to allocated 2M huge pages without specifying the node
			// we need to append 2M hugepages kernel arguments anyway, no need to add dummy
			// kernel arguments
			if page.Size == components.HugepagesSize2M {
				is2MHugepagesRequested = pointer.Bool(false)
			}

			hugepages = append(hugepages, fmt.Sprintf("hugepagesz=%s", string(page.Size)))
			hugepages = append(hugepages, fmt.Sprintf("hugepages=%d", page.Count))
		}

		// append dummy 2M huge pages kernel arguments to guarantee that the kernel will create 2M related files
		// and directories under the filesystem
		if is2MHugepagesRequested != nil && *is2MHugepagesRequested {
			if defaultHugepageSize == components.HugepagesSize1G {
				hugepages = append(hugepages, fmt.Sprintf("hugepagesz=%s", components.HugepagesSize2M))
				hugepages = append(hugepages, fmt.Sprintf("hugepages=%d", 0))
			}
		}

		hugepagesArgs := strings.Join(hugepages, cmdlineDelimiter)
		templateArgs[templateHugepages] = hugepagesArgs
	}

	if profile.Spec.AdditionalKernelArgs != nil {
		templateArgs[templateAdditionalArgs] = strings.Join(profile.Spec.AdditionalKernelArgs, cmdlineDelimiter)
	}

	if IsIRQBalancingGloballyDisabled(profile) {
		templateArgs[templateGloballyDisableIrqLoadBalancing] = strconv.FormatBool(true)
	}

	//set default [net] field first, override if needed.
	templateArgs[templateNetDevices] = fmt.Sprintf("[net]\n%s", nfConntrackHashsize)
	if profile.Spec.Net != nil && profile.Spec.Net.UserLevelNetworking != nil &&
		*profile.Spec.Net.UserLevelNetworking && profile.Spec.CPU.Reserved != nil {
		reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
		if err != nil {
			return nil, err
		}
		reserveCPUcount := reservedSet.Size()

		var devices []string
		var tunedNetDevicesOutput []string
		netPluginSequence := 0
		netPluginString := ""

		for _, device := range profile.Spec.Net.Devices {
			devices = make([]string, 0)
			if device.DeviceID != nil {
				devices = append(devices, "^ID_MODEL_ID="+*device.DeviceID)
			}
			if device.VendorID != nil {
				devices = append(devices, "^ID_VENDOR_ID="+*device.VendorID)
			}
			if device.InterfaceName != nil {
				deviceNameAmendedRegex := strings.Replace(*device.InterfaceName, "*", ".*", -1)
				if strings.HasPrefix(*device.InterfaceName, "!") {
					devices = append(devices, "^INTERFACE="+"(?!"+deviceNameAmendedRegex+")")
				} else {
					devices = append(devices, "^INTERFACE="+deviceNameAmendedRegex)
				}
			}
			// Final regex format can be one of the following formats:
			// devicesUdevRegex = ^INTERFACE=InterfaceName'        (InterfaceName can also hold .* representing * wildcard)
			// devicesUdevRegex = ^INTERFACE(?!InterfaceName)'    (InterfaceName can starting with ?! represents ! wildcard)
			// devicesUdevRegex = ^ID_VENDOR_ID=VendorID'
			// devicesUdevRegex = ^ID_MODEL_ID=DeviceID[\s\S]*^ID_VENDOR_ID=VendorID'
			// devicesUdevRegex = ^ID_MODEL_ID=DeviceID[\s\S]*^ID_VENDOR_ID=VendorID[\s\S]*^INTERFACE=InterfaceName'
			// devicesUdevRegex = ^ID_MODEL_ID=DeviceID[\s\S]*^ID_VENDOR_ID=VendorID[\s\S]*^INTERFACE=(?!InterfaceName)'
			// Important note: The order of the key must be preserved - INTERFACE, ID_MODEL_ID, ID_VENDOR_ID (in that order)
			devicesUdevRegex := strings.Join(devices, `[\s\S]*`)
			if netPluginSequence > 0 {
				netPluginString = "_" + strconv.Itoa(netPluginSequence)
			}
			tunedNetDevicesOutput = append(tunedNetDevicesOutput, fmt.Sprintf("\n[net%s]\ntype=net\ndevices_udev_regex=%s\nchannels=combined %d\n%s", netPluginString, devicesUdevRegex, reserveCPUcount, nfConntrackHashsize))
			netPluginSequence++
		}
		//nfConntrackHashsize
		if len(tunedNetDevicesOutput) == 0 {
			templateArgs[templateNetDevices] = fmt.Sprintf("[net]\nchannels=combined %d\n%s", reserveCPUcount, nfConntrackHashsize)
		} else {
			templateArgs[templateNetDevices] = strings.Join(tunedNetDevicesOutput, "")
		}
	}

	if IsRealTimeHintEnabled(profile) {
		templateArgs[templateRealTimeHint] = "true"
	}

	if IsHighPowerConsumptionHintEnabled(profile) && IsPerPodPowerManagementEnabled(profile) {
		err := fmt.Errorf("Invalid WorkloadHints configuration: HighPowerConsumption is %t and PerPodPowerManagement is %t", *profile.Spec.WorkloadHints.HighPowerConsumption, *profile.Spec.WorkloadHints.PerPodPowerManagement)
		return nil, err
	} else if IsHighPowerConsumptionHintEnabled(profile) {
		templateArgs[templateHighPowerConsumption] = "true"
	} else if IsPerPodPowerManagementEnabled(profile) {
		templateArgs[templatePerPodPowerManagement] = "true"
	}

	profileData, err := getProfileData(filepath.Join("tuned", components.ProfileNamePerformance), templateArgs)
	if err != nil {
		return nil, err
	}

	name := components.GetComponentName(profile.Name, components.ProfileNamePerformance)
	profiles := []tunedv1.TunedProfile{
		{
			Name: &name,
			Data: &profileData,
		},
	}

	priority := uint64(20)
	recommends := []tunedv1.TunedRecommend{
		{
			Profile:             &name,
			Priority:            &priority,
			MachineConfigLabels: profilecomponent.GetMachineConfigLabel(profile),
		},
	}
	return new(name, profiles, recommends), nil
}

func getProfileData(tunedTemplate string, data interface{}) (string, error) {
	profileTemplate, err := template.ParseFS(assets.Tuned, tunedTemplate)
	if err != nil {
		return "", err
	}

	profile := &bytes.Buffer{}
	if err := profileTemplate.Execute(profile, data); err != nil {
		return "", err
	}
	return profile.String(), nil
}

func IsIRQBalancingGloballyDisabled(profile *performancev2.PerformanceProfile) bool {
	return profile.Spec.GloballyDisableIrqLoadBalancing != nil && *profile.Spec.GloballyDisableIrqLoadBalancing
}

func IsRealTimeHintEnabled(profile *performancev2.PerformanceProfile) bool {
	return profile.Spec.WorkloadHints == nil || profile.Spec.WorkloadHints.RealTime == nil || *profile.Spec.WorkloadHints.RealTime
}

func IsHighPowerConsumptionHintEnabled(profile *performancev2.PerformanceProfile) bool {
	return profile.Spec.WorkloadHints != nil && profile.Spec.WorkloadHints.HighPowerConsumption != nil && *profile.Spec.WorkloadHints.HighPowerConsumption
}

func IsPerPodPowerManagementEnabled(profile *performancev2.PerformanceProfile) bool {
	return profile.Spec.WorkloadHints != nil && profile.Spec.WorkloadHints.PerPodPowerManagement != nil && *profile.Spec.WorkloadHints.PerPodPowerManagement
}
