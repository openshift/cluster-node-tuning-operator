/*
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2021 Red Hat, Inc.
 */

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/openshift/cluster-node-tuning-operator/cmd/performance-profile-creator/cmd/pkg/hypershift"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"
)

const (
	// defaultLatency refers to the fact that no additional configuration is needed
	defaultLatency string = "default"

	// lowLatency configures additional set of kernel and tuned arguments for the realtime workload
	// kernel arguments
	// 1. nohz_full=${isolated_cores}
	// 2. tsc=nowatchdog
	// 3. nosoftlockup
	// 4. nmi_watchdog=0
	// 5. mce=off
	// 6. skew_tick=1
	//
	// tuned configuration
	// 1. stalld enabled
	// 2. sched_rt_runtime_us=-1
	// 3. kernel.hung_task_timeout_secs=600
	// 4. kernel.nmi_watchdog=0
	// 5. kernel.sched_rt_runtime_us=-1
	// 6. vm.stat_interval=10
	lowLatency string = "low-latency"

	// ultraLowLatency in addition to low-latency configuration, disabling CPU P and C states
	// to guarantee that the CPU will have the lowest responsive time(also meaning high CPU consumption)
	// kernel Arguments
	// processor.max_cstate=1
	// intel_idle.max_cstate=0
	// intel_pstate=disable
	// idle=poll
	// For more information on CPU "C-states" please refer to https://gist.github.com/wmealing/2dd2b543c4d3cff6cab7
	ultraLowLatency string = "ultra-low-latency"
)

var (
	scheme                     = runtime.NewScheme()
	validTMPolicyValues        = []string{kubeletconfig.SingleNumaNodeTopologyManagerPolicy, kubeletconfig.BestEffortTopologyManagerPolicy, kubeletconfig.RestrictedTopologyManagerPolicy}
	validPowerConsumptionModes = []string{defaultLatency, lowLatency, ultraLowLatency}
	hardwareTuningMessage      = `#HardwareTuning is an advanced feature, and only intended to be used if 
#user is aware of the vendor recommendation on maximum cpu frequency.
#The structure must follow
#
# hardwareTuning:
#   isolatedCpuFreq: <Maximum frequency for applications running on isolated cpus>
#   reservedCpuFreq: <Maximum frequency for platform software running on reserved cpus>`
)

// ProfileData collects and stores all the data needed for profile creation
type ProfileData struct {
	isolatedCPUs              string
	reservedCPUs              string
	offlinedCPUs              string
	nodeSelector              *metav1.LabelSelector
	mcpSelector               map[string]string
	performanceProfileName    string
	topologyPolicy            string
	rtKernel                  bool
	additionalKernelArgs      []string
	userLevelNetworking       *bool
	disableHT                 bool
	realtimeHint              *bool
	highPowerConsumptionHint  *bool
	perPodPowerManagementHint *bool
	enableHardwareTuning      bool
	createForHypershift       bool
}

// ClusterData collects the cluster wide information, each mcp points to a list of ghw node handlers
type ClusterData map[string][]*profilecreator.GHWHandler

func init() {
	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})
	utilruntime.Must(performancev2.AddToScheme(scheme))
}

// NewRootCommand returns entrypoint command to interact with all other commands
func NewRootCommand() *cobra.Command {
	pcArgs := &ProfileCreatorArgs{
		UserLevelNetworking:   ptr.To(false),
		PerPodPowerManagement: ptr.To(false),
	}

	var requiredFlags = []string{
		"reserved-cpu-count",
		"rt-kernel",
		"must-gather-dir-path",
	}

	root := &cobra.Command{
		Use:   "performance-profile-creator",
		Short: "A tool that automates creation of Performance Profiles",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return validateMustGatherDirPath(pcArgs.MustGatherDirPath)
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			missingRequiredFlags := checkRequiredFlags(cmd, requiredFlags...)
			if len(missingRequiredFlags) > 0 {
				return fmt.Errorf("missing required flags: %s", strings.Join(argNameToFlag(missingRequiredFlags), ", "))
			}
			if err := validateProfileCreatorFlags(pcArgs); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := listNodesForPool(pcArgs)
			if err != nil {
				return err
			}
			nodesHandlers, err := makeNodesHandlers(pcArgs.MustGatherDirPath, pcArgs.NodePoolName, nodes)
			if err != nil {
				return err
			}
			if err := profilecreator.EnsureNodesHaveTheSameHardware(nodesHandlers); err != nil {
				return fmt.Errorf("targeted nodes differ: %v", err)
			}
			// We make sure that the matched Nodes are the same
			// Assumption here is moving forward matchedNodes[0] is representative of how all the nodes are
			// same from hardware topology point of view
			profileData, err := makeProfileDataFrom(nodesHandlers[0], pcArgs)
			if err != nil {
				return fmt.Errorf("failed to make profile data from node handler: %w", err)
			}
			profile, err := makePerformanceProfileFrom(*profileData)
			if err != nil {
				return err
			}
			return writeProfile(profile, profileData.enableHardwareTuning)
		},
	}
	initFlags(root.PersistentFlags(), pcArgs)

	root.AddCommand(NewInfoCommand(pcArgs))

	return root
}

func makeNodesHandlers(mustGatherDirPath, poolName string, nodes []*corev1.Node) ([]*profilecreator.GHWHandler, error) {
	handlers := make([]*profilecreator.GHWHandler, len(nodes))
	sb := strings.Builder{}
	for i, node := range nodes {
		handle, err := profilecreator.NewGHWHandler(mustGatherDirPath, node)
		if err != nil {
			return nil, fmt.Errorf("failed to load node's GHW snapshot: %w", err)
		}
		handlers[i] = handle
		sb.WriteString(node.Name + " ")
	}
	// NodePoolName is alias of MCPName
	log.Infof("Nodes names targeted by %s pool are: %s", poolName, sb.String())
	return handlers, nil
}

func initFlags(flags *pflag.FlagSet, pcArgs *ProfileCreatorArgs) {
	flags.IntVar(&pcArgs.ReservedCPUCount, "reserved-cpu-count", 0, "Number of reserved CPUs (required)")
	flags.IntVar(&pcArgs.OfflinedCPUCount, "offlined-cpu-count", 0, "Number of offlined CPUs")
	flags.BoolVar(&pcArgs.SplitReservedCPUsAcrossNUMA, "split-reserved-cpus-across-numa", false, "Split the Reserved CPUs across NUMA nodes")
	flags.StringVar(&pcArgs.MCPName, "mcp-name", "", "MCP name corresponding to the target machines (required)")
	flags.BoolVar(&pcArgs.DisableHT, "disable-ht", false, "Disable Hyperthreading")
	flags.BoolVar(&pcArgs.RTKernel, "rt-kernel", false, "Enable Real Time Kernel (required)")
	flags.BoolVar(pcArgs.UserLevelNetworking, "user-level-networking", false, "Run with User level Networking(DPDK) enabled")
	flags.StringVar(&pcArgs.PowerConsumptionMode, "power-consumption-mode", defaultLatency, fmt.Sprintf("The power consumption mode.  [Valid values: %s]", strings.Join(validPowerConsumptionModes, ", ")))
	flags.StringVar(&pcArgs.MustGatherDirPath, "must-gather-dir-path", "must-gather", "Must gather directory path")
	flags.StringVar(&pcArgs.ProfileName, "profile-name", "performance", "Name of the performance profile to be created")
	flags.StringVar(&pcArgs.TMPolicy, "topology-manager-policy", kubeletconfig.RestrictedTopologyManagerPolicy, fmt.Sprintf("Kubelet Topology Manager Policy of the performance profile to be created. [Valid values: %s, %s, %s]", kubeletconfig.SingleNumaNodeTopologyManagerPolicy, kubeletconfig.BestEffortTopologyManagerPolicy, kubeletconfig.RestrictedTopologyManagerPolicy))
	flags.BoolVar(pcArgs.PerPodPowerManagement, "per-pod-power-management", false, "Enable Per Pod Power Management")
	flags.BoolVar(&pcArgs.EnableHardwareTuning, "enable-hardware-tuning", false, "Enable setting maximum cpu frequencies")
	flags.StringVar(&pcArgs.NodePoolName, "node-pool-name", "", "Node pool name corresponding to the target machines (HyperShift only)")
}

func validateProfileCreatorFlags(pcArgs *ProfileCreatorArgs) error {
	if err := validateFlag("topology-manager-policy", pcArgs.TMPolicy, validTMPolicyValues); err != nil {
		return fmt.Errorf("invalid value for topology-manager-policy flag specified: %w", err)
	}
	if pcArgs.TMPolicy == kubeletconfig.SingleNumaNodeTopologyManagerPolicy && pcArgs.SplitReservedCPUsAcrossNUMA {
		return fmt.Errorf("not appropriate to split reserved CPUs in case of topology-manager-policy: %s", pcArgs.TMPolicy)
	}
	if err := validateFlag("power-consumption-mode", pcArgs.PowerConsumptionMode, validPowerConsumptionModes); err != nil {
		return fmt.Errorf("invalid value for power-consumption-mode flag specified: %w", err)
	}
	if pcArgs.MCPName == "" && pcArgs.NodePoolName == "" {
		return fmt.Errorf("--mcp-name or --node-pool-name options must be set")
	}
	if pcArgs.MCPName != "" && pcArgs.NodePoolName != "" {
		return fmt.Errorf("--mcp-name and --node-pool-name options cannot be used together")
	}
	if pcArgs.NodePoolName == "" {
		// NodePoolName is an alias of MCPName
		pcArgs.NodePoolName = pcArgs.MCPName
	}
	return nil
}

func checkRequiredFlags(cmd *cobra.Command, argNames ...string) []string {
	missing := []string{}
	for _, argName := range argNames {
		if !cmd.Flag(argName).Changed {
			missing = append(missing, argName)
		}
	}
	return missing
}

func argNameToFlag(argNames []string) []string {
	var flagNames []string
	for _, argName := range argNames {
		flagNames = append(flagNames, fmt.Sprintf("--%s", argName))
	}
	return flagNames
}

func makeClusterData(mustGatherDirPath string) (ClusterData, error) {
	clusterData := ClusterData{}
	nodes, err := profilecreator.GetNodeList(mustGatherDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load the cluster nodes: %v", err)
	}
	isHypershift, err := hypershift.IsHypershift(mustGatherDirPath)
	if err != nil {
		return nil, err
	}
	if isHypershift {
		nodePoolNames := sets.NewString()
		// create a set with all available nodePool names in the cluster
		for i := range nodes {
			name, ok := nodes[i].Labels[hypershift.NodePoolLabel]
			if !ok {
				continue
			}
			if !nodePoolNames.Has(name) {
				nodePoolNames.Insert(name)
			}
		}
		// classify the nodes per their matching nodePool name
		for nodePoolName := range nodePoolNames {
			var matchedNodes []*corev1.Node
			for i := range nodes {
				name, ok := nodes[i].Labels[hypershift.NodePoolLabel]
				if !ok {
					continue
				}
				if nodePoolName == name {
					matchedNodes = append(matchedNodes, nodes[i])
				}
			}
			handlers, err := makeNodesHandlers(mustGatherDirPath, nodePoolName, matchedNodes)
			if err != nil {
				return nil, err
			}
			clusterData[nodePoolName] = append(clusterData[nodePoolName], handlers...)
		}
	} else {
		mcps, err := profilecreator.GetMCPList(mustGatherDirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get the MCP list under %s: %v", mustGatherDirPath, err)
		}
		for i := range mcps {
			matchedNodes, err := profilecreator.GetNodesForPool(mcps[i], mcps, nodes)
			if err != nil {
				return nil, fmt.Errorf("failed to find MCP %s's nodes: %v", mcps[i].Name, err)
			}
			handlers, err := makeNodesHandlers(mustGatherDirPath, mcps[i].Name, matchedNodes)
			if err != nil {
				return nil, err
			}
			clusterData[mcps[i].Name] = append(clusterData[mcps[i].Name], handlers...)
		}
	}
	return clusterData, nil
}

func makeProfileDataFrom(nodeHandler *profilecreator.GHWHandler, args *ProfileCreatorArgs) (*ProfileData, error) {
	systemInfo, err := nodeHandler.GatherSystemInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to compute get system information: %v", err)
	}
	reservedCPUs, isolatedCPUs, offlinedCPUs, err := profilecreator.CalculateCPUSets(systemInfo, args.ReservedCPUCount, args.OfflinedCPUCount, args.SplitReservedCPUsAcrossNUMA, args.DisableHT, args.PowerConsumptionMode == ultraLowLatency)
	if err != nil {
		return nil, fmt.Errorf("failed to compute the reserved and isolated CPUs: %v", err)
	}
	log.Infof("%d reserved CPUs allocated: %v ", reservedCPUs.Size(), reservedCPUs.String())
	log.Infof("%d isolated CPUs allocated: %v", isolatedCPUs.Size(), isolatedCPUs.String())
	kernelArgs := profilecreator.GetAdditionalKernelArgs(args.DisableHT)
	profileData := &ProfileData{
		reservedCPUs:              reservedCPUs.String(),
		offlinedCPUs:              offlinedCPUs.String(),
		isolatedCPUs:              isolatedCPUs.String(),
		performanceProfileName:    args.ProfileName,
		topologyPolicy:            args.TMPolicy,
		rtKernel:                  args.RTKernel,
		additionalKernelArgs:      kernelArgs,
		userLevelNetworking:       args.UserLevelNetworking,
		disableHT:                 args.DisableHT,
		perPodPowerManagementHint: args.PerPodPowerManagement,
		enableHardwareTuning:      args.EnableHardwareTuning,
	}

	// setting workload hints
	switch args.PowerConsumptionMode {
	case defaultLatency:
		if profileData.rtKernel {
			return nil, fmt.Errorf(
				"%v power consumption mode is not available with real-time kernel, please use one of %v modes",
				defaultLatency, validPowerConsumptionModes[1:],
			)
		}
	case lowLatency:
		profileData.realtimeHint = ptr.To(true)
	case ultraLowLatency:
		profileData.realtimeHint = ptr.To(true)
		profileData.highPowerConsumptionHint = ptr.To(true)
		if profileData.perPodPowerManagementHint != nil && *profileData.perPodPowerManagementHint {
			return nil, fmt.Errorf(
				"please use one of %v power consumption modes together with the perPodPowerManagement",
				validPowerConsumptionModes[:2],
			)
		}
	}
	profileData.createForHypershift, err = hypershift.IsHypershift(args.MustGatherDirPath)
	if err != nil {
		return nil, err
	}
	// on Hypershift the node selector does not have an actual meaning since all the nodes associated with the
	// node pool get applied with the new profile.
	// but still we have to have a value in the node selector to pass the validation on the hypershift operator side:
	// https://github.com/openshift/hypershift/blob/bf0fa65e6f0f048e02076cf176379abbfd04134b/vendor/github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2/performanceprofile_validation.go#L203-L202
	profileData.nodeSelector = metav1.SetAsLabelSelector(map[string]string{"node-role.kubernetes.io/worker": ""})
	if !profileData.createForHypershift {
		if err := setSelectorsFor(profileData, args); err != nil {
			return nil, err
		}
	}
	return profileData, nil
}

func validateMustGatherDirPath(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("must-gather path '%s' is not valid", path)
	}
	if err != nil {
		return fmt.Errorf("can't access the must-gather path: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("must-gather path '%s' is not a directory", path)
	}
	return nil
}

func validateFlag(name, value string, validValues []string) error {
	if isStringInSlice(value, validValues) {
		return nil
	}
	return fmt.Errorf("flag %q: Value '%s' is invalid. Valid values "+
		"come from the set %v", name, value, validValues)
}

func isStringInSlice(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

// ProfileCreatorArgs represents the arguments passed to the ProfileCreator
type ProfileCreatorArgs struct {
	PowerConsumptionMode        string `json:"power-consumption-mode"`
	MustGatherDirPath           string `json:"must-gather-dir-path"`
	ProfileName                 string `json:"profile-name"`
	ReservedCPUCount            int    `json:"reserved-cpu-count"`
	OfflinedCPUCount            int    `json:"offlined-cpu-count"`
	SplitReservedCPUsAcrossNUMA bool   `json:"split-reserved-cpus-across-numa"`
	DisableHT                   bool   `json:"disable-ht"`
	RTKernel                    bool   `json:"rt-kernel"`
	UserLevelNetworking         *bool  `json:"user-level-networking,omitempty"`
	MCPName                     string `json:"mcp-name"`
	NodePoolName                string `json:"node-pool-name"`
	TMPolicy                    string `json:"topology-manager-policy"`
	PerPodPowerManagement       *bool  `json:"per-pod-power-management,omitempty"`
	EnableHardwareTuning        bool   `json:"enable-hardware-tuning,omitempty"`
}

func makePerformanceProfileFrom(profileData ProfileData) (runtime.Object, error) {
	reserved := performancev2.CPUSet(profileData.reservedCPUs)

	isolated := performancev2.CPUSet(profileData.isolatedCPUs)
	// TODO: Get the name from MCP if not specified in the command line arguments
	profile := &performancev2.PerformanceProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PerformanceProfile",
			APIVersion: performancev2.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: profileData.performanceProfileName,
		},
		Spec: performancev2.PerformanceProfileSpec{
			CPU: &performancev2.CPU{
				Isolated: &isolated,
				Reserved: &reserved,
			},
			MachineConfigPoolSelector: profileData.mcpSelector,
			NodeSelector:              profileData.nodeSelector.MatchLabels,
			RealTimeKernel: &performancev2.RealTimeKernel{
				Enabled: &profileData.rtKernel,
			},
			NUMA: &performancev2.NUMA{
				TopologyPolicy: &profileData.topologyPolicy,
			},
		},
	}

	if len(profileData.offlinedCPUs) > 0 {
		offlined := performancev2.CPUSet(profileData.offlinedCPUs)
		profile.Spec.CPU.Offlined = &offlined
	}

	if len(profileData.additionalKernelArgs) > 0 {
		profile.Spec.AdditionalKernelArgs = profileData.additionalKernelArgs
	}

	// configuring workload hints
	profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
		HighPowerConsumption:  ptr.To(false),
		RealTime:              ptr.To(false),
		PerPodPowerManagement: ptr.To(false),
	}

	if profileData.highPowerConsumptionHint != nil {
		profile.Spec.WorkloadHints.HighPowerConsumption = profileData.highPowerConsumptionHint
	}

	if profileData.realtimeHint != nil {
		profile.Spec.WorkloadHints.RealTime = profileData.realtimeHint
	}

	if profileData.perPodPowerManagementHint != nil {
		profile.Spec.WorkloadHints.PerPodPowerManagement = profileData.perPodPowerManagementHint
	}

	if profileData.userLevelNetworking != nil {
		profile.Spec.Net = &performancev2.Net{
			UserLevelNetworking: profileData.userLevelNetworking,
		}
	}
	if profileData.createForHypershift {
		yamlSerializer := serializer.NewSerializerWithOptions(
			serializer.DefaultMetaFactory, scheme, scheme,
			serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
		buff := &bytes.Buffer{}
		err := yamlSerializer.Encode(profile, buff)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize performance-profile: %w", err)
		}
		cm := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      profileData.performanceProfileName,
				Namespace: hypershift.HostedClusterNamespaceName,
			},
			Data: map[string]string{
				hypershift.ConfigMapTuningKey: buff.String(),
			},
		}
		return cm, nil
	}
	return profile, nil
}

func writeProfile(obj runtime.Object, enableHardwareTuning bool) error {
	// write CSV to out dir
	writer := strings.Builder{}
	if err := MarshallObject(obj, &writer); err != nil {
		return err
	}

	if enableHardwareTuning {
		if _, err := writer.Write([]byte(hardwareTuningMessage)); err != nil {
			return err
		}
	}

	fmt.Printf("%s", writer.String())
	return nil
}

// MarshallObject mashals an object, usually a CSV into YAML
func MarshallObject(obj interface{}, writer io.Writer) error {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	var r unstructured.Unstructured
	if err := json.Unmarshal(jsonBytes, &r.Object); err != nil {
		return err
	}

	// remove status and metadata.creationTimestamp
	unstructured.RemoveNestedField(r.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "spec", "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "status")

	deployments, exists, err := unstructured.NestedSlice(r.Object, "spec", "install", "spec", "deployments")
	if err != nil {
		return err
	}
	if exists {
		for _, obj := range deployments {
			deployment := obj.(map[string]interface{})
			unstructured.RemoveNestedField(deployment, "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "spec", "template", "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "status")
		}
		if err := unstructured.SetNestedSlice(r.Object, deployments, "spec", "install", "spec", "deployments"); err != nil {
			return err
		}
	}

	jsonBytes, err = json.Marshal(r.Object)
	if err != nil {
		return err
	}

	yamlBytes, err := yaml.JSONToYAML(jsonBytes)
	if err != nil {
		return err
	}

	// fix double quoted strings by removing unneeded single quotes...
	s := string(yamlBytes)
	s = strings.Replace(s, " '\"", " \"", -1)
	s = strings.Replace(s, "\"'\n", "\"\n", -1)

	yamlBytes = []byte(s)

	_, err = writer.Write([]byte("---\n"))
	if err != nil {
		return err
	}

	_, err = writer.Write(yamlBytes)
	if err != nil {
		return err
	}

	return nil
}

func listNodesForPool(args *ProfileCreatorArgs) ([]*corev1.Node, error) {
	nodes, err := profilecreator.GetNodeList(args.MustGatherDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load the cluster nodes: %w", err)
	}
	isHypershift, err := hypershift.IsHypershift(args.MustGatherDirPath)
	if err != nil {
		return nil, err
	}
	if isHypershift {
		var matchedNodes []*corev1.Node
		for i := range nodes {
			v, ok := nodes[i].Labels[hypershift.NodePoolLabel]
			if !ok {
				continue
			}
			if v == args.NodePoolName {
				matchedNodes = append(matchedNodes, nodes[i])
			}
		}
		return matchedNodes, nil
	}
	mcps, err := profilecreator.GetMCPList(args.MustGatherDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get the MCP list under %s: %w", args.MustGatherDirPath, err)
	}
	var selectedMCP *machineconfigv1.MachineConfigPool
	for i := range mcps {
		if mcps[i].Name == args.MCPName {
			selectedMCP = mcps[i]
		}
	}
	if selectedMCP == nil {
		return nil, fmt.Errorf("failed to find the MCP %s under must-gather path: %s", args.MCPName, args.MustGatherDirPath)
	}
	matchedNodes, err := profilecreator.GetNodesForPool(selectedMCP, mcps, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to find MCP %s's nodes: %w", selectedMCP.Name, err)
	}
	return matchedNodes, nil
}

func setSelectorsFor(profileData *ProfileData, args *ProfileCreatorArgs) error {
	mcps, err := profilecreator.GetMCPList(args.MustGatherDirPath)
	if err != nil {
		return fmt.Errorf("failed to get the MCP list under %s: %w", args.MustGatherDirPath, err)
	}
	var mcp *machineconfigv1.MachineConfigPool
	for i := range mcps {
		if mcps[i].Name == args.MCPName {
			mcp = mcps[i]
		}
	}
	if mcp == nil {
		return fmt.Errorf("failed to find the MCP %s under must-gather path: %s", args.MCPName, args.MustGatherDirPath)
	}
	mcpSelector, err := profilecreator.GetMCPSelector(mcp, mcps)
	if err != nil {
		return fmt.Errorf("failed to get the MCP selector for MCP %s: %w", args.MCPName, err)
	}
	profileData.nodeSelector = mcp.Spec.NodeSelector
	profileData.mcpSelector = mcpSelector
	return nil
}
