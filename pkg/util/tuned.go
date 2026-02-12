package util

import (
	"fmt"
	"sort"
	"strings"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

// BootcmdlineDeps returns a string containing a list of all Tuned CR names and generations
// in the format <name1>:<generation1>,<name2>:<generation2>,...<nameN>:<generationN>.
// The Tuned CR list is sorted by name for deterministic output.
// This string is used for generation-aware bootcmdline synchronization between the
// operator controller and the PerformanceProfile controller.
func BootcmdlineDeps(tunedSlice []*tunedv1.Tuned) string {
	if len(tunedSlice) == 0 {
		return ""
	}

	// Sort the Tuned CRs by name for deterministic output.
	sortedTuneds := make([]*tunedv1.Tuned, len(tunedSlice))
	copy(sortedTuneds, tunedSlice)
	sort.Slice(sortedTuneds, func(i, j int) bool {
		return sortedTuneds[i].Name < sortedTuneds[j].Name
	})

	var sb strings.Builder
	for i, tuned := range sortedTuneds {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%s:%d", tuned.Name, tuned.Generation))
	}
	return sb.String()
}
