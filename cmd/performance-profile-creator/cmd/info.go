package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"
)

const (
	infoModeJSON = "json"
	infoModeLog  = "log"
)

type infoOptions struct {
	jsonOutput bool
}

// NewInfoCommand return info command, which provides a list of the cluster nodes and their hardware topology.
// nodes are divided by their corresponding MCP/NodePool names.
func NewInfoCommand(pcArgs *ProfileCreatorArgs) *cobra.Command {
	opts := infoOptions{}
	info := &cobra.Command{
		Use:   "info",
		Short: fmt.Sprintf("requires --must-gather-dir-path, ignores other arguments. [Valid values: %s,%s]", infoModeLog, infoModeJSON),
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeInfoMode(pcArgs.MustGatherDirPath, &opts)
		},
	}
	info.Flags().BoolVar(&opts.jsonOutput, "json", false, "output as JSON")
	return info
}

func executeInfoMode(mustGatherDirPath string, infoOpts *infoOptions) error {
	nodes, err := profilecreator.GetNodeList(mustGatherDirPath)
	if err != nil {
		return fmt.Errorf("failed to load the cluster nodes: %v", err)
	}
	mcps, err := profilecreator.GetMCPList(mustGatherDirPath)
	if err != nil {
		return fmt.Errorf("failed to get the MCP list under %s: %v", mustGatherDirPath, err)
	}
	clusterData := ClusterData{}
	for _, mcp := range mcps {
		nodesHandlers, _, err := makeNodesHandlersForMCP(mustGatherDirPath, nodes, mcp.Name)
		if err != nil {
			return fmt.Errorf("failed to parse the cluster data for mcp %s: %w", mcp.Name, err)
		}
		clusterData[mcp] = nodesHandlers
	}

	clusterInfo := makeClusterInfoFromClusterData(clusterData)
	if infoOpts.jsonOutput {
		if err := showClusterInfoJSON(clusterInfo); err != nil {
			return fmt.Errorf("unable to show cluster info %w", err)
		}
	} else {
		showClusterInfoLog(clusterInfo)
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

func showClusterInfoJSON(cInfo ClusterInfo) error {
	return json.NewEncoder(os.Stdout).Encode(cInfo)
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
