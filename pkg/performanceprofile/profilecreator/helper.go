package profilecreator

import (
	v1 "k8s.io/api/core/v1"
)

// This is a linter false positive, this function is used in unit tests.
//
//nolint:unused
func newTestNode(nodeName string) *v1.Node {
	n := v1.Node{}
	n.Name = nodeName
	return &n
}
