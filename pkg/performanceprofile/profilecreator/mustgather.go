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

package profilecreator

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	v1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	// ClusterScopedResources defines the subpath, relative to the top-level must-gather directory.
	// A top-level must-gather directory is of the following format:
	// must-gather-dir/quay-io-openshift-kni-performance-addon-operator-must-gather-sha256-<Image SHA>
	// Here we find the cluster-scoped definitions saved by must-gather
	ClusterScopedResources = "cluster-scoped-resources"
	// CoreNodes defines the subpath, relative to ClusterScopedResources, on which we find node-specific data
	CoreNodes = "core/nodes"
	// MCPools defines the subpath, relative to ClusterScopedResources, on which we find the machine config pool definitions
	MCPools = "machineconfiguration.openshift.io/machineconfigpools"
	// YAMLSuffix is the extension of the yaml files saved by must-gather
	YAMLSuffix = ".yaml"
	// Nodes defines the subpath, relative to top-level must-gather directory, on which we find node-specific data
	Nodes = "nodes"
	// SysInfoFileName defines the name of the file where ghw snapshot is stored
	SysInfoFileName = "sysinfo.tgz"
	// defines the sub path relative to ClusterScopedResources, on which OCP infrastructure object is
	configOCPInfra = "config.openshift.io/infrastructures"
)

func getMustGatherFullPathsWithFilter(mustGatherPath string, suffix string, filter string) (string, error) {
	var paths []string

	// don't assume directory names, only look for the suffix, filter out files having "filter" in their names
	err := filepath.Walk(mustGatherPath, func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(path, suffix) {
			if len(filter) == 0 || !strings.Contains(path, filter) {
				paths = append(paths, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to get the path mustGatherPath:%s, suffix:%s %v", mustGatherPath, suffix, err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no match for the specified must gather directory path: %s and suffix: %s", mustGatherPath, suffix)
	}
	if len(paths) > 1 {
		Alert("Multiple matches for the specified must gather directory path: %s and suffix: %s", mustGatherPath, suffix)
		return "", fmt.Errorf("Multiple matches for the specified must gather directory path: %s and suffix: %s.\n Expected only one performance-addon-operator-must-gather* directory, please check the must-gather tarball", mustGatherPath, suffix)
	}
	// returning one possible path
	return paths[0], err
}

func getMustGatherFullPaths(mustGatherPath string, suffix string) (string, error) {
	return getMustGatherFullPathsWithFilter(mustGatherPath, suffix, "")
}

func getNode(mustGatherDirPath, nodeName string) (*v1.Node, error) {
	var node v1.Node
	nodePathSuffix := path.Join(ClusterScopedResources, CoreNodes, nodeName)
	path, err := getMustGatherFullPaths(mustGatherDirPath, nodePathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get MachineConfigPool for %s: %v", nodeName, err)
	}

	src, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %v", path, err)
	}
	defer src.Close()

	dec := k8syaml.NewYAMLOrJSONDecoder(src, 1024)
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("failed to decode %q: %v", path, err)
	}
	return &node, nil
}

// GetNodeList returns the list of nodes using the Node YAMLs stored in Must Gather
func GetNodeList(mustGatherDirPath string) ([]*v1.Node, error) {
	machines := make([]*v1.Node, 0)

	nodePathSuffix := path.Join(ClusterScopedResources, CoreNodes)
	nodePath, err := getMustGatherFullPaths(mustGatherDirPath, nodePathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get Nodes from must gather directory: %v", err)
	}
	if nodePath == "" {
		return nil, fmt.Errorf("failed to get Nodes from must gather directory: %v", err)
	}

	nodes, err := os.ReadDir(nodePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list mustGatherPath directories: %v", err)
	}
	for _, node := range nodes {
		nodeName := node.Name()
		node, err := getNode(mustGatherDirPath, nodeName)
		if err != nil {
			return nil, fmt.Errorf("failed to get Nodes %s: %v", nodeName, err)
		}
		machines = append(machines, node)
	}
	return machines, nil
}

// GetMCPList returns the list of MCPs using the mcp YAMLs stored in Must Gather
func GetMCPList(mustGatherDirPath string) ([]*machineconfigv1.MachineConfigPool, error) {
	pools := make([]*machineconfigv1.MachineConfigPool, 0)

	mcpPathSuffix := path.Join(ClusterScopedResources, MCPools)
	mcpPath, err := getMustGatherFullPaths(mustGatherDirPath, mcpPathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCPs: %v", err)
	}
	if mcpPath == "" {
		return nil, fmt.Errorf("failed to get MCPs path: %v", err)
	}

	mcpFiles, err := os.ReadDir(mcpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list mustGatherPath directories: %v", err)
	}
	for _, mcp := range mcpFiles {
		mcpName := strings.TrimSuffix(mcp.Name(), filepath.Ext(mcp.Name()))

		mcp, err := GetMCP(mustGatherDirPath, mcpName)
		// master pool relevant only when pods can be scheduled on masters, e.g. SNO
		if mcpName != "master" && err != nil {
			return nil, fmt.Errorf("can't obtain MCP %s: %v", mcpName, err)
		}
		pools = append(pools, mcp)
	}
	return pools, nil
}

// GetMCP returns an MCP object corresponding to a specified MCP Name
func GetMCP(mustGatherDirPath, mcpName string) (*machineconfigv1.MachineConfigPool, error) {
	var mcp machineconfigv1.MachineConfigPool

	mcpPathSuffix := path.Join(ClusterScopedResources, MCPools, mcpName+YAMLSuffix)
	mcpPath, err := getMustGatherFullPaths(mustGatherDirPath, mcpPathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain MachineConfigPool %s: %v", mcpName, err)
	}
	if mcpPath == "" {
		return nil, fmt.Errorf("failed to obtain MachineConfigPool, mcp:%s does not exist: %v", mcpName, err)
	}

	src, err := os.Open(mcpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %v", mcpPath, err)
	}
	defer src.Close()
	dec := k8syaml.NewYAMLOrJSONDecoder(src, 1024)
	if err := dec.Decode(&mcp); err != nil {
		return nil, fmt.Errorf("failed to decode %q: %v", mcpPath, err)
	}
	return &mcp, nil
}
