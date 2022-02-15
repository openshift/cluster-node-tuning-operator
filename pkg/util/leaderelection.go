package util

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/config/clusterstatus"
	"github.com/openshift/library-go/pkg/config/leaderelection"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

// GetLeaderElectionConfig returns leader election configs defaults based on the cluster topology
func GetLeaderElectionConfig(restcfg *rest.Config, enabled bool) configv1.LeaderElection {

	// Defaults follow conventions
	// https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#high-availability
	defaultLeaderElection := leaderelection.LeaderElectionDefaulting(
		configv1.LeaderElection{
			Disable: !enabled,
		},
		"", "",
	)

	if !enabled {
		return defaultLeaderElection
	}

	if infra, err := clusterstatus.GetClusterInfraStatus(context.TODO(), restcfg); err == nil && infra != nil {
		if infra.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
			return leaderelection.LeaderElectionSNOConfig(defaultLeaderElection)
		}
	} else {
		klog.Warningf("unable to get cluster infrastructure status, using HA cluster values for leader election: %v", err)
	}

	return defaultLeaderElection
}
