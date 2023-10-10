package profilecreator

import (
	v1 "k8s.io/api/core/v1"
)

//nolint:unused
func newTestNode(nodeName string) *v1.Node {
	n := v1.Node{}
	n.Name = nodeName
	return &n
}
