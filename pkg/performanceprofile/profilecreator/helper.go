package profilecreator

import (
	v1 "k8s.io/api/core/v1"
)

const (
	EnableHardwareTuning  = "enableHardwareTuning"
	HardwareTuningMessage = `# HardwareTuning is an advanced feature, and only intended to be used if 
# user is aware of the vendor recommendation on maximum cpu frequency.
# The structure must follow
#
# hardwareTuning:
#   isolatedCpuFreq: <Maximum frequency for applications running on isolated cpus>
#   reservedCpuFreq: <Maximum frequency for platform software running on reserved cpus>
`

	DifferentCoreIDs        = "differentCoreIDs"
	DifferentCoreIDsMessage = `# PPC tolerates having different core IDs for the same logical processors on 
# the same NUMA cell compared with other nodes that belong to the stated pool.
# While core IDs numbering may differ between two systems, it still can be considered 
# that NUMA and HW topologies are similar; However this depends on the combination 
# setting of the hardware, software and firmware as that may affect the mapping pattern. 
# While the performance profile controller depends on the logical processors per NUMA, 
# having different IDs may affect your system's performance optimization where the cores
# location matters, thus use the generated profile with caution.
`
)

// TolerationSet records the data to be tolerated or warned about based on the tool handling
type TolerationSet map[string]bool

// This is a linter false positive, this function is used in unit tests.
//
//nolint:unused
func newTestNode(nodeName string) *v1.Node {
	n := v1.Node{}
	n.Name = nodeName
	return &n
}
