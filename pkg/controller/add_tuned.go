package controller

import (
	"github.com/openshift/cluster-node-tuning-operator/pkg/controller/tuned"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, tuned.Add)
}
