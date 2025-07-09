package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const (
	infoModeJSON = "json"
	infoModeLog  = "log"
)

type infoOptions struct {
	jsonOutput bool
	textOutput bool
}

// NewInfoCommand return info command, which provides a list of the cluster nodes and their hardware topology.
// nodes are divided by their corresponding MCP/NodePool names.
func NewInfoCommand(pcArgs *ProfileCreatorArgs) *cobra.Command {
	opts := infoOptions{}
	info := &cobra.Command{
		Use:   "info",
		Short: fmt.Sprintf("requires --must-gather-dir-path, ignores other arguments. [Valid values: %s,%s]", infoModeLog, infoModeJSON),
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeInfoMode(pcArgs.MustGatherDirPath, pcArgs.createForHypershift, &opts)
		},
	}
	info.Flags().BoolVar(&opts.jsonOutput, "json", false, "output as JSON")
	info.Flags().BoolVar(&opts.textOutput, "text", false, "output as plain text")
	return info
}

func executeInfoMode(mustGatherDirPath string, createForHypershift bool, infoOpts *infoOptions) error {
	clusterData, err := makeClusterData(mustGatherDirPath, createForHypershift)
	if err != nil {
		return fmt.Errorf("failed to parse the cluster data: %w", err)
	}
	clusterInfo := makeClusterInfoFromClusterData(clusterData)
	if err := showClusterInfo(clusterInfo, infoOpts); err != nil {
		return fmt.Errorf("unable to show cluster info %w", err)
	}
	return nil
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
	for poolName, nodeHandlers := range cluster {
		mInfo := MCPInfo{
			Name: poolName,
		}
		for _, handle := range nodeHandlers {
			topology, err := handle.SortedTopology()
			if err != nil {
				Alert("%s(Topology discovery error: %v)", handle.Node.GetName(), err)
				continue
			}

			htEnabled, err := handle.IsHyperthreadingEnabled()
			if err != nil {
				Alert("%s(HT discovery error: %v)", handle.Node.GetName(), err)
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

func showClusterInfo(cInfo ClusterInfo, infoOpts *infoOptions) error {
	if infoOpts.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(cInfo)
	}
	textInfo := dumpClusterInfo(cInfo)
	if infoOpts.textOutput {
		fmt.Print(textInfo)
		return nil
	}
	Alert("Cluster info:\n%s", textInfo)
	return nil
}

func dumpClusterInfo(cInfo ClusterInfo) string {
	var sb strings.Builder
	for _, mcpInfo := range cInfo {
		fmt.Fprintf(&sb, "MCP '%s' nodes:", mcpInfo.Name)
		sb.WriteString("\n")
		for _, nInfo := range mcpInfo.Nodes {
			fmt.Fprintf(&sb, "Node: %s (NUMA cells: %d, HT: %v)", nInfo.Name, len(nInfo.NUMACells), nInfo.HTEnabled)
			sb.WriteString("\n")
			for _, cInfo := range nInfo.NUMACells {
				fmt.Fprintf(&sb, "NUMA cell %d : %v", cInfo.ID, cInfo.CoreList)
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "CPU(s): %d", nInfo.CPUsCount)
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "---")
		sb.WriteString("\n")
	}
	return sb.String()
}
