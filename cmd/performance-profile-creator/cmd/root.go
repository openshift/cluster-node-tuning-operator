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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"

	"github.com/spf13/cobra"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"
)

const (
	infoModeJSON = "json"
	infoModeLog  = "log"
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
	validTMPolicyValues        = []string{kubeletconfig.SingleNumaNodeTopologyManagerPolicy, kubeletconfig.BestEffortTopologyManagerPolicy, kubeletconfig.RestrictedTopologyManagerPolicy}
	validInfoModes             = []string{infoModeLog, infoModeJSON}
	validPowerConsumptionModes = []string{defaultLatency, lowLatency, ultraLowLatency}
)

// ProfileData collects and stores all the data needed for profile creation
type ProfileData struct {
	isolatedCPUs             string
	reservedCPUs             string
	nodeSelector             *metav1.LabelSelector
	mcpSelector              map[string]string
	performanceProfileName   string
	topologyPolicy           string
	rtKernel                 bool
	additionalKernelArgs     []string
	userLevelNetworking      *bool
	disableHT                bool
	realtimeHint             *bool
	highPowerConsumptionHint *bool
}

// ClusterData collects the cluster wide information, each mcp points to a list of ghw node handlers
type ClusterData map[*machineconfigv1.MachineConfigPool][]*profilecreator.GHWHandler

func init() {
	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})
}

// NewRootCommand returns entrypoint command to interact with all other commands
func NewRootCommand() *cobra.Command {
	pcArgs := &ProfileCreatorArgs{
		UserLevelNetworking: pointer.BoolPtr(false),
	}

	var requiredFlags = []string{
		"reserved-cpu-count",
		"mcp-name",
		"rt-kernel",
		"must-gather-dir-path",
	}

	root := &cobra.Command{
		Use:   "performance-profile-creator",
		Short: "A tool that automates creation of Performance Profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flag("info").Changed {
				infoMode := cmd.Flag("info").Value.String()
				if err := validateFlag("info", infoMode, validInfoModes); err != nil {
					return err
				}

				missingRequiredFlags := checkRequiredFlags(cmd, "must-gather-dir-path")
				if len(missingRequiredFlags) > 0 {
					return fmt.Errorf("missing required flags: %s", strings.Join(argNameToFlag(missingRequiredFlags), ", "))
				}

				mustGatherDirPath := cmd.Flag("must-gather-dir-path").Value.String()
				cluster, err := getClusterData(mustGatherDirPath)
				if err != nil {
					return fmt.Errorf("failed to parse the cluster data: %v", err)
				}

				clusterInfo := makeClusterInfoFromClusterData(cluster)
				if infoMode == infoModeJSON {
					showClusterInfoJSON(clusterInfo)
				} else {
					showClusterInfoLog(clusterInfo)
				}
				return nil
			}

			missingRequiredFlags := checkRequiredFlags(cmd, requiredFlags...)
			if len(missingRequiredFlags) > 0 {
				return fmt.Errorf("missing required flags: %s", strings.Join(argNameToFlag(missingRequiredFlags), ", "))
			}

			mustGatherDirPath := cmd.Flag("must-gather-dir-path").Value.String()
			cluster, err := getClusterData(mustGatherDirPath)
			if err != nil {
				return fmt.Errorf("failed to parse the cluster data: %v", err)
			}

			profileCreatorArgsFromFlags, err := getDataFromFlags(cmd)
			if err != nil {
				return fmt.Errorf("failed to obtain data from flags %v", err)
			}
			profileData, err := getProfileData(profileCreatorArgsFromFlags, cluster)
			if err != nil {
				return err
			}

			err = createProfile(*profileData)
			return err
		},
	}

	root.PersistentFlags().IntVar(&pcArgs.ReservedCPUCount, "reserved-cpu-count", 0, "Number of reserved CPUs (required)")
	root.PersistentFlags().BoolVar(&pcArgs.SplitReservedCPUsAcrossNUMA, "split-reserved-cpus-across-numa", false, "Split the Reserved CPUs across NUMA nodes")
	root.PersistentFlags().StringVar(&pcArgs.MCPName, "mcp-name", "", "MCP name corresponding to the target machines (required)")
	root.PersistentFlags().BoolVar(&pcArgs.DisableHT, "disable-ht", false, "Disable Hyperthreading")
	root.PersistentFlags().BoolVar(&pcArgs.RTKernel, "rt-kernel", false, "Enable Real Time Kernel (required)")
	root.PersistentFlags().BoolVar(pcArgs.UserLevelNetworking, "user-level-networking", false, "Run with User level Networking(DPDK) enabled")
	root.PersistentFlags().StringVar(&pcArgs.PowerConsumptionMode, "power-consumption-mode", defaultLatency, fmt.Sprintf("The power consumption mode.  [Valid values: %s]", strings.Join(validPowerConsumptionModes, ", ")))
	root.PersistentFlags().StringVar(&pcArgs.MustGatherDirPath, "must-gather-dir-path", "must-gather", "Must gather directory path")
	root.PersistentFlags().StringVar(&pcArgs.ProfileName, "profile-name", "performance", "Name of the performance profile to be created")
	root.PersistentFlags().StringVar(&pcArgs.TMPolicy, "topology-manager-policy", kubeletconfig.RestrictedTopologyManagerPolicy, fmt.Sprintf("Kubelet Topology Manager Policy of the performance profile to be created. [Valid values: %s, %s, %s]", kubeletconfig.SingleNumaNodeTopologyManagerPolicy, kubeletconfig.BestEffortTopologyManagerPolicy, kubeletconfig.RestrictedTopologyManagerPolicy))
	root.PersistentFlags().StringVar(&pcArgs.Info, "info", infoModeLog, fmt.Sprintf("Show cluster information; requires --must-gather-dir-path, ignore the other arguments. [Valid values: %s]", strings.Join(validInfoModes, ", ")))

	return root
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

func getClusterData(mustGatherDirPath string) (ClusterData, error) {
	cluster := make(ClusterData)
	info, err := os.Stat(mustGatherDirPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("the must-gather path '%s' is not valid", mustGatherDirPath)
	}
	if err != nil {
		return nil, fmt.Errorf("can't access the must-gather path: %v", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("the must-gather path '%s' is not a directory", mustGatherDirPath)
	}

	mcps, err := profilecreator.GetMCPList(mustGatherDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get the MCP list under %s: %v", mustGatherDirPath, err)
	}

	nodes, err := profilecreator.GetNodeList(mustGatherDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load the cluster nodes: %v", err)
	}

	for _, mcp := range mcps {
		matchedNodes, err := profilecreator.GetNodesForPool(mcp, mcps, nodes)
		if err != nil {
			return nil, fmt.Errorf("failed to find MCP %s's nodes: %v", mcp.Name, err)
		}
		handlers := make([]*profilecreator.GHWHandler, len(matchedNodes))
		for i, node := range matchedNodes {
			handle, err := profilecreator.NewGHWHandler(mustGatherDirPath, node)
			if err != nil {
				return nil, fmt.Errorf("failed to load node's %s's GHW snapshot : %v", mcp.Name, err)
			}
			handlers[i] = handle
		}
		cluster[mcp] = handlers
	}

	return cluster, nil
}

// NUMACellInfo describe a NUMA cell on a node
type NUMACellInfo struct {
	ID       int   `json:"id"`
	CoreList []int `json:"cores"`
}

// NodeInfo describe a Node in a MCP
type NodeInfo struct {
	Name      string         `json:"name"`
	HTEnabled bool           `json:"smt_enabled"`
	CPUsCount int            `json:"cpus_count"`
	NUMACells []NUMACellInfo `json:"numa_cells"`
}

// MCPInfo describe a MCP in a cluster
type MCPInfo struct {
	Name  string     `json:"name"`
	Nodes []NodeInfo `json:"nodes"`
}

// ClusterInfo describe a cluster
type ClusterInfo []MCPInfo

// Sort ensures all sequences in the ClusterInfo are sorted, to make comparisons easier.
func (cInfo ClusterInfo) Sort() ClusterInfo {
	for _, mcpInfo := range cInfo {
		for _, nodeInfo := range mcpInfo.Nodes {
			for _, numaCell := range nodeInfo.NUMACells {
				sort.Ints(numaCell.CoreList)
			}
			sort.Slice(nodeInfo.NUMACells, func(i, j int) bool { return nodeInfo.NUMACells[i].ID < nodeInfo.NUMACells[j].ID })
		}
	}
	sort.Slice(cInfo, func(i, j int) bool { return cInfo[i].Name < cInfo[j].Name })
	return cInfo
}

func makeClusterInfoFromClusterData(cluster ClusterData) ClusterInfo {
	var cInfo ClusterInfo
	for mcp, nodeHandlers := range cluster {
		mInfo := MCPInfo{
			Name: mcp.Name,
		}
		for _, handle := range nodeHandlers {
			topology, err := handle.SortedTopology()
			if err != nil {
				log.Infof("%s(Topology discovery error: %v)", handle.Node.GetName(), err)
				continue
			}

			htEnabled, err := handle.IsHyperthreadingEnabled()
			if err != nil {
				log.Infof("%s(HT discovery error: %v)", handle.Node.GetName(), err)
			}

			nInfo := NodeInfo{
				Name:      handle.Node.GetName(),
				HTEnabled: htEnabled,
			}

			for id, node := range topology.Nodes {
				var coreList []int
				for _, core := range node.Cores {
					coreList = append(coreList, core.LogicalProcessors...)
				}
				nInfo.CPUsCount += len(coreList)
				nInfo.NUMACells = append(nInfo.NUMACells, NUMACellInfo{
					ID:       id,
					CoreList: coreList,
				})
			}
			mInfo.Nodes = append(mInfo.Nodes, nInfo)
		}
		cInfo = append(cInfo, mInfo)
	}
	return cInfo.Sort()
}

func showClusterInfoJSON(cInfo ClusterInfo) {
	json.NewEncoder(os.Stdout).Encode(cInfo)
}

func showClusterInfoLog(cInfo ClusterInfo) {
	log.Infof("Cluster info:")
	for _, mcpInfo := range cInfo {
		log.Infof("MCP '%s' nodes:", mcpInfo.Name)
		for _, nInfo := range mcpInfo.Nodes {
			log.Infof("Node: %s (NUMA cells: %d, HT: %v)", nInfo.Name, len(nInfo.NUMACells), nInfo.HTEnabled)
			for _, cInfo := range nInfo.NUMACells {
				log.Infof("NUMA cell %d : %v", cInfo.ID, cInfo.CoreList)
			}
			log.Infof("CPU(s): %d", nInfo.CPUsCount)
		}
		log.Infof("---")
	}
}
func getDataFromFlags(cmd *cobra.Command) (ProfileCreatorArgs, error) {
	creatorArgs := ProfileCreatorArgs{}
	mustGatherDirPath := cmd.Flag("must-gather-dir-path").Value.String()
	mcpName := cmd.Flag("mcp-name").Value.String()
	reservedCPUCount, err := strconv.Atoi(cmd.Flag("reserved-cpu-count").Value.String())
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse reserved-cpu-count flag: %v", err)
	}
	splitReservedCPUsAcrossNUMA, err := strconv.ParseBool(cmd.Flag("split-reserved-cpus-across-numa").Value.String())
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse split-reserved-cpus-across-numa flag: %v", err)
	}
	profileName := cmd.Flag("profile-name").Value.String()
	tmPolicy := cmd.Flag("topology-manager-policy").Value.String()
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse topology-manager-policy flag: %v", err)
	}
	err = validateFlag("topology-manager-policy", tmPolicy, validTMPolicyValues)
	if err != nil {
		return creatorArgs, fmt.Errorf("invalid value for topology-manager-policy flag specified: %v", err)
	}
	if tmPolicy == kubeletconfig.SingleNumaNodeTopologyManagerPolicy && splitReservedCPUsAcrossNUMA {
		return creatorArgs, fmt.Errorf("not appropriate to split reserved CPUs in case of topology-manager-policy: %v", tmPolicy)
	}
	powerConsumptionMode := cmd.Flag("power-consumption-mode").Value.String()
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse power-consumption-mode flag: %v", err)
	}
	err = validateFlag("power-consumption-mode", powerConsumptionMode, validPowerConsumptionModes)
	if err != nil {
		return creatorArgs, fmt.Errorf("invalid value for power-consumption-mode flag specified: %v", err)
	}

	rtKernelEnabled, err := strconv.ParseBool(cmd.Flag("rt-kernel").Value.String())
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse rt-kernel flag: %v", err)
	}

	htDisabled, err := strconv.ParseBool(cmd.Flag("disable-ht").Value.String())
	if err != nil {
		return creatorArgs, fmt.Errorf("failed to parse disable-ht flag: %v", err)
	}
	creatorArgs = ProfileCreatorArgs{
		MustGatherDirPath:           mustGatherDirPath,
		ProfileName:                 profileName,
		ReservedCPUCount:            reservedCPUCount,
		SplitReservedCPUsAcrossNUMA: splitReservedCPUsAcrossNUMA,
		MCPName:                     mcpName,
		TMPolicy:                    tmPolicy,
		RTKernel:                    rtKernelEnabled,
		PowerConsumptionMode:        powerConsumptionMode,
		DisableHT:                   htDisabled,
	}

	if cmd.Flag("user-level-networking").Changed {
		userLevelNetworkingEnabled, err := strconv.ParseBool(cmd.Flag("user-level-networking").Value.String())
		if err != nil {
			return creatorArgs, fmt.Errorf("failed to parse user-level-networking flag: %v", err)
		}
		creatorArgs.UserLevelNetworking = &userLevelNetworkingEnabled
	}

	return creatorArgs, nil
}

func getProfileData(args ProfileCreatorArgs, cluster ClusterData) (*ProfileData, error) {
	mcps := make([]*machineconfigv1.MachineConfigPool, len(cluster))
	mcpNames := make([]string, len(cluster))
	var mcp *machineconfigv1.MachineConfigPool

	i := 0
	for m := range cluster {
		mcps[i] = m
		mcpNames[i] = m.Name
		if m.Name == args.MCPName {
			mcp = m
		}
		i++
	}

	if mcp == nil {
		return nil, fmt.Errorf("'%s' MCP does not exist, valid values are %v", args.MCPName, mcpNames)
	}

	mcpSelector, err := profilecreator.GetMCPSelector(mcp, mcps)
	if err != nil {
		return nil, fmt.Errorf("failed to compute the MCP selector: %v", err)
	}

	if len(cluster[mcp]) == 0 {
		return nil, fmt.Errorf("no schedulable nodes are associated with '%s' MCP", args.MCPName)
	}

	var matchedNodeNames []string
	for _, nodeHandler := range cluster[mcp] {
		matchedNodeNames = append(matchedNodeNames, nodeHandler.Node.GetName())
	}
	log.Infof("Nodes targetted by %s MCP are: %v", args.MCPName, matchedNodeNames)
	err = profilecreator.EnsureNodesHaveTheSameHardware(cluster[mcp])
	if err != nil {
		return nil, fmt.Errorf("targeted nodes differ: %v", err)
	}

	// We make sure that the matched Nodes are the same
	// Assumption here is moving forward matchedNodes[0] is representative of how all the nodes are
	// same from hardware topology point of view

	nodeHandle := cluster[mcp][0]
	reservedCPUs, isolatedCPUs, err := nodeHandle.GetReservedAndIsolatedCPUs(args.ReservedCPUCount, args.SplitReservedCPUsAcrossNUMA, args.DisableHT)
	if err != nil {
		return nil, fmt.Errorf("failed to compute the reserved and isolated CPUs: %v", err)
	}
	log.Infof("%d reserved CPUs allocated: %v ", reservedCPUs.Size(), reservedCPUs.String())
	log.Infof("%d isolated CPUs allocated: %v", isolatedCPUs.Size(), isolatedCPUs.String())
	kernelArgs := profilecreator.GetAdditionalKernelArgs(args.DisableHT)
	profileData := &ProfileData{
		reservedCPUs:           reservedCPUs.String(),
		isolatedCPUs:           isolatedCPUs.String(),
		nodeSelector:           mcp.Spec.NodeSelector,
		mcpSelector:            mcpSelector,
		performanceProfileName: args.ProfileName,
		topologyPolicy:         args.TMPolicy,
		rtKernel:               args.RTKernel,
		additionalKernelArgs:   kernelArgs,
		userLevelNetworking:    args.UserLevelNetworking,
	}

	// setting workload hints
	switch args.PowerConsumptionMode {
	case defaultLatency:
		if profileData.rtKernel {
			return nil, fmt.Errorf(
				"please use one of %v power consumption modes together with the real-time kernel",
				validPowerConsumptionModes[1:],
			)
		}
	case lowLatency:
		profileData.realtimeHint = pointer.BoolPtr(true)
	case ultraLowLatency:
		profileData.realtimeHint = pointer.BoolPtr(true)
		profileData.highPowerConsumptionHint = pointer.BoolPtr(true)

	}
	return profileData, nil
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
	SplitReservedCPUsAcrossNUMA bool   `json:"split-reserved-cpus-across-numa"`
	DisableHT                   bool   `json:"disable-ht"`
	RTKernel                    bool   `json:"rt-kernel"`
	UserLevelNetworking         *bool  `json:"user-level-networking,omitempty"`
	MCPName                     string `json:"mcp-name"`
	TMPolicy                    string `json:"topology-manager-policy"`
	Info                        string `json:"info"`
}

func createProfile(profileData ProfileData) error {
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

	if len(profileData.additionalKernelArgs) > 0 {
		profile.Spec.AdditionalKernelArgs = profileData.additionalKernelArgs
	}

	// configuring workload hints
	profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
		HighPowerConsumption: pointer.BoolPtr(false),
		RealTime:             pointer.BoolPtr(false),
	}
	if profileData.highPowerConsumptionHint != nil {
		profile.Spec.WorkloadHints.HighPowerConsumption = profileData.highPowerConsumptionHint
	}

	if profileData.realtimeHint != nil {
		profile.Spec.WorkloadHints.RealTime = profileData.realtimeHint
	}

	if profileData.userLevelNetworking != nil {
		profile.Spec.Net = &performancev2.Net{
			UserLevelNetworking: profileData.userLevelNetworking,
		}
	}

	// write CSV to out dir
	writer := strings.Builder{}
	if err := MarshallObject(&profile, &writer); err != nil {
		return err
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
	if exists {
		for _, obj := range deployments {
			deployment := obj.(map[string]interface{})
			unstructured.RemoveNestedField(deployment, "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "spec", "template", "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "status")
		}
		unstructured.SetNestedSlice(r.Object, deployments, "spec", "install", "spec", "deployments")
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
