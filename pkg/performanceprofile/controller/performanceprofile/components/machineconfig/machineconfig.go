package machineconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"text/template"

	assets "github.com/openshift/cluster-node-tuning-operator/assets/performanceprofile"

	"github.com/coreos/go-systemd/unit"
	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"

	apiconfigv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	defaultIgnitionVersion       = "3.2.0"
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
)

const (
	// MCKernelRT is the value of the kernel setting in MachineConfig for the RT kernel
	MCKernelRT = "realtime"
	// MCKernelDefault is the value of the kernel setting in MachineConfig for the default kernel
	MCKernelDefault = "default"
	// HighPerformanceRuntime contains the name of the high-performance runtime
	HighPerformanceRuntime = "high-performance"

	bashScriptsDir     = "/usr/local/bin"
	crioConfd          = "/etc/crio/crio.conf.d"
	crioRuntimesConfig = "99-runtimes.conf"

	// Workload partitioning configs
	kubernetesConfDir      = "/etc/kubernetes"
	crioPartitioningConfig = "99-workload-pinning.conf"
	ocpPartitioningConfig  = "openshift-workload-pinning"

	// OCIHooksConfigDir is the default directory for the OCI hooks
	OCIHooksConfigDir = "/etc/containers/oci/hooks.d"
	// OCIHooksConfig file contains the low latency hooks configuration
	ociTemplateRPSMask   = "RPSMask"
	udevRulesDir         = "/etc/udev/rules.d"
	udevRpsRules         = "99-netdev-rps.rules"
	udevPhysicalRpsRules = "99-netdev-physical-rps.rules"
	// scripts
	hugepagesAllocation       = "hugepages-allocation"
	setCPUsOffline            = "set-cpus-offline"
	setRPSMask                = "set-rps-mask"
	clearIRQBalanceBannedCPUs = "clear-irqbalance-banned-cpus"
	cpusetConfigure           = "cpuset-configure"
)

const (
	systemdSectionUnit     = "Unit"
	systemdSectionService  = "Service"
	systemdSectionInstall  = "Install"
	systemdDescription     = "Description"
	systemdBefore          = "Before"
	systemdEnvironment     = "Environment"
	systemdType            = "Type"
	systemdRemainAfterExit = "RemainAfterExit"
	systemdExecStart       = "ExecStart"
	systemdWantedBy        = "WantedBy"
)

const (
	systemdServiceIRQBalance   = "irqbalance.service"
	systemdServiceKubelet      = "kubelet.service"
	systemdServiceCrio         = "crio.service"
	systemdServiceTypeOneshot  = "oneshot"
	systemdTargetMultiUser     = "multi-user.target"
	systemdTargetNetworkOnline = "network-online.target"
	systemdTrue                = "true"
)

const (
	environmentHugepagesSize  = "HUGEPAGES_SIZE"
	environmentHugepagesCount = "HUGEPAGES_COUNT"
	environmentNUMANode       = "NUMA_NODE"
	environmentOfflineCpus    = "OFFLINE_CPUS"
)

const (
	templateReservedCpus = "ReservedCpus"
	templateWorkload     = "Workload"
	templateRuntimePath  = "RuntimePath"
	templateRuntimeRoot  = "RuntimeRoot"
)

// New returns new machine configuration object for performance sensitive workloads
func New(profile *performancev2.PerformanceProfile, pinningMode *apiconfigv1.CPUPartitioningMode, defaultRuntime machineconfigv1.ContainerRuntimeDefaultRuntime) (*machineconfigv1.MachineConfig, error) {
	name := GetMachineConfigName(profile)
	mc := &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: profilecomponent.GetMachineConfigLabel(profile),
		},
		Spec: machineconfigv1.MachineConfigSpec{},
	}

	ignitionConfig, err := getIgnitionConfig(profile, pinningMode, defaultRuntime)
	if err != nil {
		return nil, err
	}

	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}

	enableRTKernel := profile.Spec.RealTimeKernel != nil &&
		profile.Spec.RealTimeKernel.Enabled != nil &&
		*profile.Spec.RealTimeKernel.Enabled

	if enableRTKernel {
		mc.Spec.KernelType = MCKernelRT
	} else {
		mc.Spec.KernelType = MCKernelDefault
	}

	return mc, nil
}

// GetMachineConfigName generates machine config name from the performance profile
func GetMachineConfigName(profile *performancev2.PerformanceProfile) string {
	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	return fmt.Sprintf("50-%s", name)
}

func getIgnitionConfig(profile *performancev2.PerformanceProfile, pinningMode *apiconfigv1.CPUPartitioningMode, defaultRuntime machineconfigv1.ContainerRuntimeDefaultRuntime) (*igntypes.Config, error) {
	var scripts []string
	ignitionConfig := &igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: defaultIgnitionVersion,
		},
		Storage: igntypes.Storage{
			Files: []igntypes.File{},
		},
	}

	// add script files under the node /usr/local/bin directory
	if profileutil.IsRpsEnabled(profile) || profile.Spec.WorkloadHints == nil ||
		profile.Spec.WorkloadHints.RealTime == nil || *profile.Spec.WorkloadHints.RealTime {
		scripts = []string{hugepagesAllocation, setRPSMask, setCPUsOffline, clearIRQBalanceBannedCPUs}
	} else {
		// realtime is explicitly disabled by workload hint
		scripts = []string{hugepagesAllocation, setCPUsOffline, clearIRQBalanceBannedCPUs}
	}
	mode := 0700
	for _, script := range scripts {
		dst := getBashScriptPath(script)
		content, err := assets.Scripts.ReadFile(fmt.Sprintf("scripts/%s.sh", script))
		if err != nil {
			return nil, err
		}
		addContent(ignitionConfig, content, dst, &mode)
	}

	// add crio config snippet under the node /etc/crio/crio.conf.d/ directory
	crioConfdRuntimesMode := 0644
	crioConfigSnippetContent, err := renderCrioConfigSnippet(profile, defaultRuntime, filepath.Join("configs", crioRuntimesConfig))
	if err != nil {
		return nil, err
	}
	crioConfSnippetDst := filepath.Join(crioConfd, crioRuntimesConfig)
	addContent(ignitionConfig, crioConfigSnippetContent, crioConfSnippetDst, &crioConfdRuntimesMode)

	// do not add RPS handling when realtime is explicitly disabled by workload hint
	if profileutil.IsRpsEnabled(profile) || profile.Spec.WorkloadHints == nil ||
		profile.Spec.WorkloadHints.RealTime == nil || *profile.Spec.WorkloadHints.RealTime {

		// add rps udev rule
		rpsRulesMode := 0644
		var rpsRulesContent []byte
		if profileutil.IsPhysicalRpsEnabled(profile) {
			rpsRulesContent, err = assets.Configs.ReadFile(filepath.Join("configs", udevPhysicalRpsRules))
		} else {
			rpsRulesContent, err = assets.Configs.ReadFile(filepath.Join("configs", udevRpsRules))
		}
		if err != nil {
			return nil, err
		}
		rpsRulesDst := filepath.Join(udevRulesDir, udevRpsRules)
		addContent(ignitionConfig, rpsRulesContent, rpsRulesDst, &rpsRulesMode)

		if profile.Spec.CPU != nil && profile.Spec.CPU.Reserved != nil {
			rpsMask, err := components.CPUListToMaskList(string(*profile.Spec.CPU.Reserved))
			if err != nil {
				return nil, err
			}

			rpsService, err := getSystemdContent(getRPSUnitOptions(rpsMask))
			if err != nil {
				return nil, err
			}

			ignitionConfig.Systemd.Units = append(ignitionConfig.Systemd.Units, igntypes.Unit{
				Contents: &rpsService,
				Name:     getSystemdService("update-rps@"),
			})
		}
	}

	if profile.Spec.HugePages != nil {
		for _, page := range profile.Spec.HugePages.Pages {
			// we already allocated non NUMA specific hugepages via kernel arguments
			if page.Node == nil {
				continue
			}

			hugepagesSize, err := GetHugepagesSizeKilobytes(page.Size)
			if err != nil {
				return nil, err
			}

			hugepagesService, err := getSystemdContent(getHugepagesAllocationUnitOptions(
				hugepagesSize,
				page.Count,
				*page.Node,
			))
			if err != nil {
				return nil, err
			}

			ignitionConfig.Systemd.Units = append(ignitionConfig.Systemd.Units, igntypes.Unit{
				Contents: &hugepagesService,
				Enabled:  pointer.BoolPtr(true),
				Name:     getSystemdService(fmt.Sprintf("%s-%skB-NUMA%d", hugepagesAllocation, hugepagesSize, *page.Node)),
			})
		}
	}

	if profile.Spec.CPU != nil && profile.Spec.CPU.Reserved != nil {
		// Workload partitioning specific configuration
		clusterIsPinned := pinningMode != nil && *pinningMode == apiconfigv1.CPUPartitioningAllNodes
		if clusterIsPinned {
			crioPartitionFileData, err := renderManagementCPUPinningConfig(profile.Spec.CPU, crioPartitioningConfig)
			if err != nil {
				return nil, err
			}
			crioPartitionDst := filepath.Join(crioConfd, crioPartitioningConfig)
			addContent(ignitionConfig, crioPartitionFileData, crioPartitionDst, &crioConfdRuntimesMode)

			ocpPartitionFileData, err := renderManagementCPUPinningConfig(profile.Spec.CPU, ocpPartitioningConfig)
			if err != nil {
				return nil, err
			}
			ocpPartitionDst := filepath.Join(kubernetesConfDir, ocpPartitioningConfig)
			addContent(ignitionConfig, ocpPartitionFileData, ocpPartitionDst, &crioConfdRuntimesMode)
		}

		// Support for cpu balancing configuration on RHEL 9 with cgroupv1
		cpusetConfigureService, err := getSystemdContent(getCpusetConfigureServiceOptions())
		if err != nil {
			return nil, err
		}

		ignitionConfig.Systemd.Units = append(ignitionConfig.Systemd.Units, igntypes.Unit{
			Contents: &cpusetConfigureService,
			Enabled:  pointer.BoolPtr(true),
			Name:     getSystemdService(cpusetConfigure),
		})

		dst := getBashScriptPath(cpusetConfigure)
		content, err := assets.Scripts.ReadFile(fmt.Sprintf("scripts/%s.sh", cpusetConfigure))
		if err != nil {
			return nil, err
		}
		addContent(ignitionConfig, content, dst, &mode)
	}

	if profile.Spec.CPU.Offlined != nil {
		offlinedCPUSList, err := cpuset.Parse(string(*profile.Spec.CPU.Offlined))
		if err != nil {
			return nil, err
		}
		offlinedCPUSstring := components.ListToString(offlinedCPUSList.ToSlice())
		offlineCPUsService, err := getSystemdContent(getOfflineCPUs(offlinedCPUSstring))
		if err != nil {
			return nil, err
		}

		ignitionConfig.Systemd.Units = append(ignitionConfig.Systemd.Units, igntypes.Unit{
			Contents: &offlineCPUsService,
			Enabled:  pointer.BoolPtr(true),
			Name:     getSystemdService(setCPUsOffline),
		})
	}

	clearIRQBalanceBannedCPUsService, err := getSystemdContent(getIRQBalanceBannedCPUsOptions())
	if err != nil {
		return nil, err
	}

	ignitionConfig.Systemd.Units = append(ignitionConfig.Systemd.Units, igntypes.Unit{
		Contents: &clearIRQBalanceBannedCPUsService,
		Enabled:  pointer.BoolPtr(true),
		Name:     getSystemdService(clearIRQBalanceBannedCPUs),
	})

	return ignitionConfig, nil
}

func getBashScriptPath(scriptName string) string {
	return fmt.Sprintf("%s/%s.sh", bashScriptsDir, scriptName)
}

func getSystemdEnvironment(key string, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

func getSystemdService(serviceName string) string {
	return fmt.Sprintf("%s.service", serviceName)
}

func getSystemdContent(options []*unit.UnitOption) (string, error) {
	outReader := unit.Serialize(options)
	outBytes, err := ioutil.ReadAll(outReader)
	if err != nil {
		return "", err
	}
	return string(outBytes), nil
}

// GetOCIHooksConfigContent reads and returns the content of the OCI hook file
func GetOCIHooksConfigContent(configFile string, profile *performancev2.PerformanceProfile) ([]byte, error) {
	ociHookConfigTemplate, err := template.ParseFS(assets.Configs, filepath.Join("configs", configFile))
	if err != nil {
		return nil, err
	}

	rpsMask := "0" // RPS disabled
	if profile.Spec.CPU != nil && profile.Spec.CPU.Reserved != nil {
		rpsMask, err = components.CPUListToMaskList(string(*profile.Spec.CPU.Reserved))
		if err != nil {
			return nil, err
		}
	}

	outContent := &bytes.Buffer{}
	templateArgs := map[string]string{ociTemplateRPSMask: rpsMask}
	if err := ociHookConfigTemplate.Execute(outContent, templateArgs); err != nil {
		return nil, err
	}

	return outContent.Bytes(), nil
}

// GetHugepagesSizeKilobytes retruns hugepages size in kilobytes
func GetHugepagesSizeKilobytes(hugepagesSize performancev2.HugePageSize) (string, error) {
	switch hugepagesSize {
	case "1G":
		return "1048576", nil
	case "2M":
		return "2048", nil
	default:
		return "", fmt.Errorf("can not convert size %q to kilobytes", hugepagesSize)
	}
}

func getCpusetConfigureServiceOptions() []*unit.UnitOption {
	return []*unit.UnitOption{
		// [Unit]
		// Description
		unit.NewUnitOption(systemdSectionUnit, systemdDescription, "Move services to reserved cpuset"),
		// Before
		unit.NewUnitOption(systemdSectionUnit, systemdBefore, systemdTargetNetworkOnline),
		// Type
		unit.NewUnitOption(systemdSectionService, systemdType, systemdServiceTypeOneshot),
		// ExecStart
		unit.NewUnitOption(systemdSectionService, systemdExecStart, getBashScriptPath(cpusetConfigure)),
		// [Install]
		// WantedBy
		unit.NewUnitOption(systemdSectionInstall, systemdWantedBy, systemdTargetMultiUser+" "+systemdServiceCrio),
	}
}

func getIRQBalanceBannedCPUsOptions() []*unit.UnitOption {
	return []*unit.UnitOption{
		// [Unit]
		// Description
		unit.NewUnitOption(systemdSectionUnit, systemdDescription, "Clear the IRQBalance Banned CPU mask early in the boot"),
		// Before
		unit.NewUnitOption(systemdSectionUnit, systemdBefore, systemdServiceKubelet),
		unit.NewUnitOption(systemdSectionUnit, systemdBefore, systemdServiceIRQBalance),
		// [Service]
		// Type
		unit.NewUnitOption(systemdSectionService, systemdType, systemdServiceTypeOneshot),
		// RemainAfterExit
		unit.NewUnitOption(systemdSectionService, systemdRemainAfterExit, systemdTrue),
		// ExecStart
		unit.NewUnitOption(systemdSectionService, systemdExecStart, getBashScriptPath(clearIRQBalanceBannedCPUs)),
		// [Install]
		// WantedBy
		unit.NewUnitOption(systemdSectionInstall, systemdWantedBy, systemdTargetMultiUser),
	}
}

func getHugepagesAllocationUnitOptions(hugepagesSize string, hugepagesCount int32, numaNode int32) []*unit.UnitOption {
	return []*unit.UnitOption{
		// [Unit]
		// Description
		unit.NewUnitOption(systemdSectionUnit, systemdDescription, fmt.Sprintf("Hugepages-%skB allocation on the node %d", hugepagesSize, numaNode)),
		// Before
		unit.NewUnitOption(systemdSectionUnit, systemdBefore, systemdServiceKubelet),
		// [Service]
		// Environment
		unit.NewUnitOption(systemdSectionService, systemdEnvironment, getSystemdEnvironment(environmentHugepagesCount, fmt.Sprint(hugepagesCount))),
		unit.NewUnitOption(systemdSectionService, systemdEnvironment, getSystemdEnvironment(environmentHugepagesSize, hugepagesSize)),
		unit.NewUnitOption(systemdSectionService, systemdEnvironment, getSystemdEnvironment(environmentNUMANode, fmt.Sprint(numaNode))),
		// Type
		unit.NewUnitOption(systemdSectionService, systemdType, systemdServiceTypeOneshot),
		// RemainAfterExit
		unit.NewUnitOption(systemdSectionService, systemdRemainAfterExit, systemdTrue),
		// ExecStart
		unit.NewUnitOption(systemdSectionService, systemdExecStart, getBashScriptPath(hugepagesAllocation)),
		// [Install]
		// WantedBy
		unit.NewUnitOption(systemdSectionInstall, systemdWantedBy, systemdTargetMultiUser),
	}
}

func getOfflineCPUs(offlineCpus string) []*unit.UnitOption {
	return []*unit.UnitOption{
		// [Unit]
		// Description
		unit.NewUnitOption(systemdSectionUnit, systemdDescription, fmt.Sprintf("Set cpus offline: %s", offlineCpus)),
		// Before
		unit.NewUnitOption(systemdSectionUnit, systemdBefore, systemdServiceKubelet),
		// Environment
		unit.NewUnitOption(systemdSectionService, systemdEnvironment, getSystemdEnvironment(environmentOfflineCpus, offlineCpus)),
		// Type
		unit.NewUnitOption(systemdSectionService, systemdType, systemdServiceTypeOneshot),
		// RemainAfterExit
		unit.NewUnitOption(systemdSectionService, systemdRemainAfterExit, systemdTrue),
		// ExecStart
		unit.NewUnitOption(systemdSectionService, systemdExecStart, getBashScriptPath(setCPUsOffline)),
		// [Install]
		// WantedBy
		unit.NewUnitOption(systemdSectionInstall, systemdWantedBy, systemdTargetMultiUser),
	}
}

func getRPSUnitOptions(rpsMask string) []*unit.UnitOption {
	cmd := fmt.Sprintf("%s %%i %s", getBashScriptPath(setRPSMask), rpsMask)
	return []*unit.UnitOption{
		// [Unit]
		// Description
		unit.NewUnitOption(systemdSectionUnit, systemdDescription, "Sets network devices RPS mask"),
		// [Service]
		// Type
		unit.NewUnitOption(systemdSectionService, systemdType, systemdServiceTypeOneshot),
		// ExecStart
		unit.NewUnitOption(systemdSectionService, systemdExecStart, cmd),
	}
}

func addContent(ignitionConfig *igntypes.Config, content []byte, dst string, mode *int) {
	contentBase64 := base64.StdEncoding.EncodeToString(content)
	ignitionConfig.Storage.Files = append(ignitionConfig.Storage.Files, igntypes.File{
		Node: igntypes.Node{
			Path: dst,
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Contents: igntypes.Resource{
				Source: pointer.StringPtr(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, contentBase64)),
			},
			Mode: mode,
		},
	})
}

func renderCrioConfigSnippet(profile *performancev2.PerformanceProfile, defaultRuntime machineconfigv1.ContainerRuntimeDefaultRuntime, src string) ([]byte, error) {
	templateArgs := map[string]string{
		templateRuntimePath: "/bin/runc",
		templateRuntimeRoot: "/run/runc",
	}

	if profile.Spec.CPU.Reserved != nil {
		templateArgs[templateReservedCpus] = string(*profile.Spec.CPU.Reserved)
	}

	if defaultRuntime == machineconfigv1.ContainerRuntimeDefaultRuntimeCrun {
		templateArgs[templateRuntimePath] = "/usr/bin/crun"
		templateArgs[templateRuntimeRoot] = "/run/crun"
	}

	profileTemplate, err := template.ParseFS(assets.Configs, src)
	if err != nil {
		return nil, err
	}

	crioConfig := &bytes.Buffer{}
	if err := profileTemplate.Execute(crioConfig, templateArgs); err != nil {
		return nil, err
	}

	return crioConfig.Bytes(), nil
}

// Render out the CPU pinning configuration for CRIO and Kubelet
// The rendered files will make use of the `reserved` CPUs to generate the configuration files.
// The template files listed below
// [./assets/performanceprofile/configs/99-workload-pinning.conf] - CRI-O Config
// [./assets/performanceprofile/configs/openshift-workload-pinning] - Kubelet Config
//
// Note:
// The CRI-O Config for `cpushares` must be set to zero.
// Not setting it causes the TOML to not be parsed by CRI-O which is why the template value is set to zero.
// Further, it should not be configurable through the API, as the Kubelet will inject the correct cpu share annotations according to the pod spec.
// Carried patches in https://github.com/openshift/kubernetes/pull/706
func renderManagementCPUPinningConfig(cpuv2 *performancev2.CPU, src string) ([]byte, error) {
	if cpuv2 == nil {
		return nil, fmt.Errorf("cpu value is required, skipping generating file")
	}
	// In the future we might enhance this to allow more annotations, currently it's assumed
	// this will only be related to management "Infrastrcture" workloads
	vars := map[string]string{
		templateWorkload:     "management",
		templateReservedCpus: string(*cpuv2.Reserved),
	}
	embdedFilePath := filepath.Join("configs", src)

	profileTemplate, err := template.ParseFS(assets.Configs, embdedFilePath)
	if err != nil {
		return nil, err
	}

	pinningConfigData := &bytes.Buffer{}
	if err := profileTemplate.Execute(pinningConfigData, vars); err != nil {
		return nil, err
	}

	return pinningConfigData.Bytes(), nil
}

func BootstrapWorkloadPinningMC(role string, pinningMode *apiconfigv1.CPUPartitioningMode) (*machineconfigv1.MachineConfig, error) {
	if pinningMode == nil {
		return nil, fmt.Errorf("can not generate configs, CPUPartitioningMode is nil")
	}

	if *pinningMode != apiconfigv1.CPUPartitioningAllNodes {
		return nil, nil
	}

	mode := 420
	source := "data:text/plain;charset=utf-8;base64,ewogICJtYW5hZ2VtZW50IjogewogICAgImNwdXNldCI6ICIiCiAgfQp9Cg=="

	mc := &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("01-%s-cpu-partitioning", role),
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": role,
			},
		},
		Spec: machineconfigv1.MachineConfigSpec{},
	}

	ignitionConfig := &igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: igntypes.MaxVersion.String(),
		},
		Storage: igntypes.Storage{
			Files: []igntypes.File{{
				Node: igntypes.Node{
					Path: "/etc/kubernetes/openshift-workload-pinning",
				},
				FileEmbedded1: igntypes.FileEmbedded1{
					Contents: igntypes.Resource{
						Source: &source,
					},
					Mode: &mode,
				},
			}},
		},
	}

	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return mc, nil
}
