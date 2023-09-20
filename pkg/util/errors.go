package util

import (
	"errors"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
)

// IsNoMatchError checks if error is for a non existant resource, there is a meaningful difference between a
// resource type not existing on the cluster as a whole versus an individual resource not being found.
//
// Example:
// OLM can be an optional operator, when the OLM resources do not exist, the returned
// error is a discovery error of meta.NoResourceMatchError.
//
// A bug is present in controller-runtime@v0.16.1 and older where the returned error type is a DiscoveryFailedError
// this was fixed in https://github.com/kubernetes-sigs/controller-runtime/pull/2472 and versions of controller-runtime@v0.16.2
// going forward will return the meta.NoResourceMatchError error. Here we check if either one is true.
func IsNoMatchError(err error) bool {
	// We use errors.As instead of discovery.IsGroupDiscoveryFailedError because it Unwraps errors.
	_err := &discovery.ErrGroupDiscoveryFailed{}
	return meta.IsNoMatchError(err) || errors.As(err, &_err)
}
