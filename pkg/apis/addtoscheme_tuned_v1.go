package apis

import (
	"github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

func init() {
	// Register the types with the Scheme so the components can map objects to GroupVersionKinds and back
	AddToSchemes = append(AddToSchemes, v1.SchemeBuilder.AddToScheme)
}
