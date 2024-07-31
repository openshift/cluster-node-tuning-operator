package hypershift

import (
	"fmt"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"
)

const NodePoolLabel = "hypershift.openshift.io/nodePool"

func IsHypershift(mustGatherPath string) (bool, error) {
	isHypershift, err := profilecreator.IsExternalControlPlaneCluster(mustGatherPath)
	if err != nil {
		return false, fmt.Errorf("failed to determine if hypershift cluster: %w", err)
	}
	return isHypershift, nil
}
