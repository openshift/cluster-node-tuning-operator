package cpuset

import (
	"os"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"github.com/golang/glog"
)

// MustParse CPUSet constructs a new CPU set from a Linux CPU list formatted
// string.
// It panics if the input cannot be used to construct a CPU set.
func MustParse(s string) cpuset.CPUSet {
	res, err := cpuset.Parse(s)
	if err != nil {
		glog.Fatalf("failed to parse input as CPUSet; input: %q err: %v", s, err)
		os.Exit(1)
	}
	return res
}
