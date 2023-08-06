package machineconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
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

	// sysctl config
	defaultRPSMaskConfig  = "99-default-rps-mask.conf"
	sysctlConfigDir       = "/etc/sysctl.d/"
	sysctlTemplateRPSMask = "RPSMask"

	// Workload partitioning configs
	kubernetesConfDir      = "/etc/kubernetes"
	crioPartitioningConfig = "99-workload-pinning.conf"
	ocpPartitioningConfig  = "openshift-workload-pinning"

	udevRulesDir         = "/etc/udev/rules.d"
	udevPhysicalRpsRules = "99-netdev-physical-rps.rules"
	// scripts
	hugepagesAllocation       = "hugepages-allocation"
	setCPUsOffline            = "set-cpus-offline"
	setRPSMask                = "set-rps-mask"
	clearIRQBalanceBannedCPUs = "clear-irqbalance-banned-cpus"

	ovsSliceName                     = "ovs.slice"
	ovsDynamicPinningTriggerFile     = "ovs-enable-dynamic-cpu-affinity"
	ovsDynamicPinningTriggerHostFile = "/etc/openvswitch/enable_dynamic_cpu_affinity"

	cpusetConfigure = "cpuset-configure"
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
	templateReservedCpus           = "ReservedCpus"
	templateOvsSliceName           = "OvsSliceName"
	templateOvsSliceDefinitionFile = "ovs.slice"
	templateOvsSliceUsageFile      = "01-use-ovs-slice.conf"
	templateWorkload               = "Workload"
	templateRuntimePath            = "RuntimePath"
	templateRuntimeRoot            = "RuntimeRoot"
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

		// configure default rps mask applied to all network devices
		sysctlConfContent, err := renderSysctlConf(profile, filepath.Join("configs", defaultRPSMaskConfig))
		if err != nil {
			return nil, err
		}
		sysctlConfFileMode := 0644
		sysctlConfDst := filepath.Join(sysctlConfigDir, defaultRPSMaskConfig)
		addContent(ignitionConfig, sysctlConfContent, sysctlConfDst, &sysctlConfFileMode)

		// if RPS disabled for physical devices revert the default RPS mask to 0
		if !profileutil.IsPhysicalRpsEnabled(profile) {
			// add rps udev rule
			rpsRulesMode := 0644
			var rpsRulesContent []byte
			rpsRulesContent, err = assets.Configs.ReadFile(filepath.Join("configs", udevPhysicalRpsRules))

			if err != nil {
				return nil, err
			}
			rpsRulesDst := filepath.Join(udevRulesDir, udevPhysicalRpsRules)
			addContent(ignitionConfig, rpsRulesContent, rpsRulesDst, &rpsRulesMode)

			rpsService, err := getSystemdContent(getRPSUnitOptions("0"))
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
		content, err := getTemplatedOvsFile(assets.Scripts, fmt.Sprintf("scripts/%s.sh", cpusetConfigure), ovsSliceName)
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
		offlinedCPUSstring := components.ListToString(offlinedCPUSList.List())
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

	if ok, ovsSliceName := MoveOvsIntoOwnSlice(); ok {
		// Create the OVS slice that will lift the cpu restrictions for better kernel networking performance
		// This is technically not necessary as systemd is smart enough
		// to create a slice when a unit references it.
		// However, this allows us to set the resources allocated and the cpu balancing

		ovsCgroupUnit, err := getOvsSliceDefinition(ovsSliceName)
		if err != nil {
			return nil, err
		}

		ovsMode := 0644
		addContent(ignitionConfig, ovsCgroupUnit, "/etc/systemd/system/"+ovsSliceName, &ovsMode)

		// Configure OVS services to use the newly created slice
		serviceOvsSlice, err := getOvsSliceUsage(ovsSliceName)
		if err != nil {
			return nil, err
		}

		addContent(ignitionConfig, serviceOvsSlice, "/etc/systemd/system/openvswitch.service.d/"+templateOvsSliceUsageFile, &ovsMode)
		addContent(ignitionConfig, serviceOvsSlice, "/etc/systemd/system/ovs-vswitchd.service.d/"+templateOvsSliceUsageFile, &ovsMode)
		addContent(ignitionConfig, serviceOvsSlice, "/etc/systemd/system/ovsdb-server.service.d/"+templateOvsSliceUsageFile, &ovsMode)

		// Tell OVN-K to enable dynamic cpu pinning
		content, err := getTemplatedOvsFile(assets.Configs, filepath.Join("configs", ovsDynamicPinningTriggerFile), ovsSliceName)
		if err != nil {
			return nil, err
		}
		addContent(ignitionConfig, content, ovsDynamicPinningTriggerHostFile, &ovsMode)
	}

	return ignitionConfig, nil
}

func MoveOvsIntoOwnSlice() (bool, string) {
	// Make sure this does not interfere with SNO and workload partitioning
	// where OVS is intentionally still running in reserved only due to
	// workload partitioning restricting the cpuset for OVN-K pods.
	//
	// This will be propagated by the ovkube-node cpu affinity logic
	// and restrict OVS to reserved only.
	return true, ovsSliceName
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

func getTemplatedOvsFile(fsys fs.FS, templateName string, name string) ([]byte, error) {
	templateArgs := make(map[string]string)
	templateArgs[templateOvsSliceName] = name

	sliceTemplate, err := template.ParseFS(fsys, templateName)
	if err != nil {
		return nil, err
	}

	slice := &bytes.Buffer{}
	if err := sliceTemplate.Execute(slice, templateArgs); err != nil {
		return nil, err
	}

	return slice.Bytes(), nil
}

func getOvsSliceDefinition(name string) ([]byte, error) {
	return getTemplatedOvsFile(assets.Configs, filepath.Join("configs", templateOvsSliceDefinitionFile), name)
}

func getOvsSliceUsage(name string) ([]byte, error) {
	return getTemplatedOvsFile(assets.Configs, filepath.Join("configs", templateOvsSliceUsageFile), name)
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

// BootstrapWorkloadPinningMC creates an initial state MachineConfig resource that establishes an empty CPU set for both
// CRIO config and Kubelet config. The purpose is provide empty state initialization for both CRIO and Kubelet so that the nodes
// always start and join the cluster in a Workload Pinning configuration.
//
// When a performance profiles is created, they will override the config files in this MC with the desired CPU Set. If that performance
// profile were to be deleted later on, this initial MC will be the fallback that MCO re-renders and maintain a workload pinning configuration.
func BootstrapWorkloadPinningMC(role string, pinningMode *apiconfigv1.CPUPartitioningMode) (*machineconfigv1.MachineConfig, error) {
	if pinningMode == nil {
		return nil, fmt.Errorf("can not generate configs, CPUPartitioningMode is nil")
	}

	if *pinningMode != apiconfigv1.CPUPartitioningAllNodes {
		return nil, nil
	}

	mode := 420
	emptySet := performancev2.CPUSet("")
	emptySetCPU := performancev2.CPU{Reserved: &emptySet}

	ocpPartitionEmptySetFileData, err := renderManagementCPUPinningConfig(&emptySetCPU, ocpPartitioningConfig)
	if err != nil {
		return nil, err
	}

	crioPartitionEmptySetFileData, err := renderManagementCPUPinningConfig(&emptySetCPU, crioPartitioningConfig)
	if err != nil {
		return nil, err
	}

	// We add lower hierarchical config file name so as to not disturb any
	// pre-existing files that users might have for workload pinning
	crioDefaultPartitioningConfig := "01-workload-pinning-default.conf"

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
	}

	// Add empty set kubelet configuration file
	addContent(
		ignitionConfig,
		ocpPartitionEmptySetFileData,
		filepath.Join(kubernetesConfDir, ocpPartitioningConfig),
		&mode)
	// Add empty set crio configuration file
	addContent(
		ignitionConfig,
		crioPartitionEmptySetFileData,
		filepath.Join(crioConfd, crioDefaultPartitioningConfig),
		&mode)

	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return mc, nil
}

func renderSysctlConf(profile *performancev2.PerformanceProfile, src string) ([]byte, error) {
	if profile.Spec.CPU == nil || profile.Spec.CPU.Reserved == nil {
		return nil, nil
	}

	rpsMask, err := components.CPUListToMaskList(string(*profile.Spec.CPU.Reserved))
	if err != nil {
		return nil, err
	}

	templateArgs := map[string]string{
		sysctlTemplateRPSMask: rpsMask,
	}

	sysctlConfigTemplate, err := template.ParseFS(assets.Configs, src)
	if err != nil {
		return nil, err
	}

	sysctlConfig := &bytes.Buffer{}
	if err = sysctlConfigTemplate.Execute(sysctlConfig, templateArgs); err != nil {
		return nil, err
	}

	return sysctlConfig.Bytes(), nil
}
