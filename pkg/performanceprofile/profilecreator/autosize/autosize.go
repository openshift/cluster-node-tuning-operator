package autosize

import "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"

// shortcut
var Alert = profilecreator.Alert

type Params struct {
	NodePoolSize     int
	OfflinedCPUCount int
}

type Values struct {
	ReservedCPUCount int
}

func Compute(params Params, user Values) (Values, error) {
	if err := reservedCPUCountIfNeeded(params, &user); err != nil {
		return user, nil
	}
	return user, nil
}

func reservedCPUCountIfNeeded(params Params, vals *Values) error {
	if vals.ReservedCPUCount > 0 {
		Alert("using user-provided reserved CPU count: %d", vals.ReservedCPUCount)
		return nil
	}
	Alert("autosizing reserved CPU count for %d nodes", params.NodePoolSize)
	vals.ReservedCPUCount = 1

	Alert("autosizing reserved CPU result: %d", vals.ReservedCPUCount)
	return nil
}
